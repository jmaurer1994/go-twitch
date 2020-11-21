package pubsub

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Adeithe/go-twitch/pubsub/nonce"
	"github.com/gorilla/websocket"
)

// Conn stores data about a PubSub connection
type Conn struct {
	length int
	socket *websocket.Conn
	done   chan bool

	isConnected bool
	latency     time.Duration
	ping        chan bool

	topics    map[string][]string
	pending   map[string]chan error
	nonces    sync.Mutex
	listeners sync.Mutex
	writer    sync.Mutex

	onMessage    []func(MessageData)
	onPong       []func(time.Duration)
	onReconnect  []func()
	onDisconnect []func()
}

// IConn interface for methods used by the PubSub connection
type IConn interface {
	Connect() error
	Reconnect() error
	Write(int, []byte) error
	WriteMessage(MessageType, interface{}) error
	WriteMessageWithNonce(MessageType, string, interface{}) error
	Close()

	IsConnected() bool
	SetMaxTopics(int)
	GetNumTopics() int
	HasTopic(string) bool

	Listen(...string) error
	ListenWithAuth(string, ...string) error
	Unlisten(...string) error
	Ping() (time.Duration, error)

	OnMessage(func(MessageData))
	OnPong(func(time.Duration))
	OnReconnect(func())
	OnDisconnect(func())
}

var _ IConn = &Conn{}

// IP for the PubSub server
const IP = "pubsub-edge.twitch.tv"

// Connect to the PubSub server
func (conn *Conn) Connect() error {
	u := url.URL{Scheme: "wss", Host: IP}
	socket, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return err
	}
	if conn.length < 1 {
		conn.length = 50
	}
	conn.socket = socket
	conn.done = make(chan bool)
	conn.isConnected = true
	go conn.reader()
	go conn.ticker()
	if conn.topics != nil {
		var wg sync.WaitGroup
		conn.listeners.Lock()
		rejoined := make(map[string][]string)
		for token, topics := range conn.topics {
			wg.Add(1)
			go func(token string, topics ...string) {
				if err := conn.ListenWithAuth(token, topics...); err == nil {
					rejoined[token] = topics
				}
				wg.Done()
			}(token, topics...)
		}
		conn.listeners.Unlock()
		wg.Wait()
		conn.listeners.Lock()
		defer conn.listeners.Unlock()
		conn.topics = rejoined
	}
	return nil
}

// Reconnect to the PubSub server
func (conn *Conn) Reconnect() error {
	if conn.isConnected {
		conn.Close()
	}
	if err := conn.Connect(); err != nil {
		return err
	}
	for _, f := range conn.onReconnect {
		go f()
	}
	return nil
}

// Write a message and send it to the server
func (conn *Conn) Write(msgType int, data []byte) error {
	conn.writer.Lock()
	defer conn.writer.Unlock()
	return conn.socket.WriteMessage(msgType, data)
}

// WriteMessage with no nonce and send it to the server
func (conn *Conn) WriteMessage(msgType MessageType, data interface{}) error {
	msg := Packet{
		Type: msgType,
		Data: data,
	}
	bytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return conn.Write(websocket.TextMessage, bytes)
}

// WriteMessageWithNonce write a message with the provided nonce and send it to the server
//
// This operation will block, giving the server up to 5 seconds to respond after correcting for latency before failing
func (conn *Conn) WriteMessageWithNonce(msgType MessageType, nonce string, data interface{}) error {
	msg := Packet{
		Type:  msgType,
		Nonce: nonce,
		Data:  data,
	}
	bytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if err := conn.Write(websocket.TextMessage, bytes); err != nil {
		return err
	}
	conn.nonces.Lock()
	nc := make(chan error, 1)
	if conn.pending == nil {
		conn.pending = make(map[string]chan error)
	}
	conn.pending[nonce] = nc
	conn.nonces.Unlock()
	select {
	case ex := <-nc:
		err = ex
	case <-time.After(time.Second*5 + conn.latency):
		err = ErrNonceTimeout
	}
	conn.nonces.Lock()
	close(nc)
	delete(conn.pending, nonce)
	conn.nonces.Unlock()
	return err
}

// Close the connection to the PubSub server
func (conn *Conn) Close() {
	conn.Write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	select {
	case <-conn.done:
	case <-time.After(time.Second):
		conn.socket.Close()
		close(conn.done)
	}
}

// IsConnected returns true if the socket is actively connected
func (conn *Conn) IsConnected() bool {
	return conn.isConnected
}

// SetMaxTopics changes the maximum number of topics the connection can listen to
func (conn *Conn) SetMaxTopics(max int) {
	if max < 1 {
		max = 50
	}
	conn.length = max
}

// GetNumTopics returns the number of topics the connection is actively listening to
func (conn *Conn) GetNumTopics() (n int) {
	conn.listeners.Lock()
	defer conn.listeners.Unlock()
	if conn.topics != nil {
		for _, topics := range conn.topics {
			n += len(topics)
		}
	}
	return
}

// HasTopic returns true if the connection is actively listening to the provided topic
func (conn *Conn) HasTopic(topic string) bool {
	conn.listeners.Lock()
	defer conn.listeners.Unlock()
	for _, g := range conn.topics {
		for _, t := range g {
			if topic == t {
				return true
			}
		}
	}
	return false
}

// Listen to a topic using no authentication token
//
// This operation will block, giving the server up to 5 seconds to respond after correcting for latency before failing
func (conn *Conn) Listen(topics ...string) error {
	if conn.GetNumTopics()+len(topics) > conn.length {
		return ErrShardTooManyTopics
	}
	if err := conn.WriteMessageWithNonce(Listen, nonce.New(), TopicData{Topics: topics}); err != nil {
		return err
	}
	conn.listeners.Lock()
	defer conn.listeners.Unlock()
	if conn.topics == nil {
		conn.topics = make(map[string][]string)
	}
	conn.topics[""] = append(conn.topics[""], topics...)
	return nil
}

// ListenWithAuth starts listening to a topic using the provided authentication token
//
// This operation will block, giving the server up to 5 seconds to respond after correcting for latency before failing
func (conn *Conn) ListenWithAuth(token string, topics ...string) error {
	data := TopicData{
		Topics: topics,
		Token:  token,
	}
	if err := conn.WriteMessageWithNonce(Listen, nonce.New(), data); err != nil {
		return err
	}
	conn.listeners.Lock()
	defer conn.listeners.Unlock()
	if conn.topics == nil {
		conn.topics = make(map[string][]string)
	}
	conn.topics[token] = append(conn.topics[token], topics...)
	return nil
}

// Unlisten from the provided topics
//
// This operation will block, giving the server up to 5 seconds to respond after correcting for latency before failing
func (conn *Conn) Unlisten(topics ...string) error {
	var unlisten []string
	for _, topic := range topics {
		if conn.HasTopic(topic) {
			unlisten = append(unlisten, topic)
		}
	}
	if len(unlisten) < 1 {
		return nil
	}
	conn.listeners.Lock()
	for token, topics := range conn.topics {
		var new []string
		for _, topic := range topics {
			var b bool
			for _, t := range unlisten {
				if topic == t {
					b = true
					break
				}
			}
			if !b {
				new = append(new, topic)
			}
		}
		conn.topics[token] = new
	}
	conn.listeners.Unlock()
	if err := conn.WriteMessageWithNonce(Unlisten, nonce.New(), TopicData{Topics: unlisten}); err != nil {
		return err
	}
	return nil
}

// Ping the PubSub server
//
// This operation will block, giving the server up to 5 seconds to respond after correcting for latency before failing
func (conn *Conn) Ping() (time.Duration, error) {
	start := time.Now()
	conn.ping = make(chan bool, 1)
	if err := conn.WriteMessage(Ping, nil); err != nil {
		return 0, err
	}
	select {
	case <-conn.ping:
	case <-time.After(time.Second*5 + conn.latency):
		return 0, ErrPingTimeout
	}
	conn.latency = time.Since(start)
	for _, f := range conn.onPong {
		go f(conn.latency)
	}
	return conn.latency, nil
}

// OnMessage event called after a message is receieved
func (conn *Conn) OnMessage(f func(MessageData)) {
	conn.onMessage = append(conn.onMessage, f)
}

// OnPong event called after a Pong message is received, updating the latency
func (conn *Conn) OnPong(f func(time.Duration)) {
	conn.onPong = append(conn.onPong, f)
}

// OnReconnect event called after the connection is reopened
func (conn *Conn) OnReconnect(f func()) {
	conn.onReconnect = append(conn.onReconnect, f)
}

// OnDisconnect event called after the connection is closed
func (conn *Conn) OnDisconnect(f func()) {
	conn.onDisconnect = append(conn.onDisconnect, f)
}

func (conn *Conn) reader() {
	for {
		msgType, bytes, err := conn.socket.ReadMessage()
		if err != nil || msgType == websocket.CloseMessage {
			break
		}
		var msg Packet
		if err := json.Unmarshal(bytes, &msg); err != nil {
			continue
		}
		if len(msg.Nonce) > 0 && conn.pending != nil && len(conn.pending) > 0 {
			var err error
			conn.nonces.Lock()
			c, ok := conn.pending[msg.Nonce]
			conn.nonces.Unlock()
			if len(msg.Error) > 0 && ok {
				switch msg.Error {
				case BadMessage:
					err = ErrBadMessage
				case BadAuth:
					err = ErrBadAuth
				case TooManyTopics:
					err = ErrShardTooManyTopics
				case BadTopic, InvalidTopic:
					err = ErrBadTopic
				case ServerError:
					err = ErrServer
				default:
					fmt.Printf("Uncaught PubSub Error: %s\n", msg.Error)
					err = ErrUnknown
				}
			}
			c <- err
		}
		switch msg.Type {
		case Response:
		case Message:
			data := MessageData{}
			bytes, _ := json.Marshal(msg.Data)
			if err := json.Unmarshal(bytes, &data); err != nil {
				break
			}
			for _, f := range conn.onMessage {
				go f(data)
			}
		case Pong:
			close(conn.ping)
		case Reconnect:
			conn.Reconnect()
			return
		default:
			fmt.Println(strings.TrimSpace(string(bytes)))
		}
	}
	conn.socket.Close()
	close(conn.done)
	for _, f := range conn.onDisconnect {
		go f()
	}
}

func (conn *Conn) ticker() {
	for {
		select {
		case <-conn.done:
			return
		case <-time.After(time.Minute * 5):
			conn.Ping()
		}
	}
}
