package api

import (
    "bytes"
	"context"
	"net/http"
    "encoding/json"
)

type Channel struct {
	ID                          string   `json:"broadcaster_id"`
	Login                       string   `json:"broadcaster_login"`
	DisplayName                 string   `json:"broadcaster_name"`
	GameID                      string   `json:"game_id"`
	GameName                    string   `json:"game_name"`
	Title                       string   `json:"title"`
	Delay                       int      `json:"delay"`
	Tags                        []string `json:"tags"`
	ContentClassificationLabels []string `json:"content_classification_labels"`
	IsBrandedContent            bool     `json:"is_branded_content"`
}

type ChannelsResource struct {
	client *Client
}

func NewChannelsResource(client *Client) *ChannelsResource {
	return &ChannelsResource{client}
}

type ChannelsListCall struct {
	resource *ChannelsResource
	opts     []RequestOption
}

type ChannelsListResponse struct {
	Header http.Header
	Data   []Channel
}
type ChannelsUpdateCall struct {
    resource *ChannelsResource
    channel  Channel
    opts     []RequestOption
}

// SetTitle sets the title for the channel update.
func (c *ChannelsUpdateCall) SetTitle(title string) *ChannelsUpdateCall {
    c.channel.Title = title
    return c
}

// SetGameID sets the game ID for the channel update.
func (c *ChannelsUpdateCall) SetGameID(gameID string) *ChannelsUpdateCall {
    c.channel.GameID = gameID
    return c
}

// SetDelay sets the delay for the channel update.
func (c *ChannelsUpdateCall) SetDelay(delay int) *ChannelsUpdateCall {
    c.channel.Delay = delay
    return c
}

// SetTags sets the tags for the channel update.
func (c *ChannelsUpdateCall) SetTags(tags []string) *ChannelsUpdateCall {
    c.channel.Tags = tags
    return c
}

// SetIsBrandedContent sets the branded content flag for the channel update.
func (c *ChannelsUpdateCall) SetIsBrandedContent(isBrandedContent bool) *ChannelsUpdateCall {
    c.channel.IsBrandedContent = isBrandedContent
    return c
}
// List creates a request to list channels based on the specified criteria.
func (r *ChannelsResource) List() *ChannelsListCall {
	return &ChannelsListCall{resource: r}
}
func (r *ChannelsResource) Update(broadcasterID string) *ChannelsUpdateCall {
    return &ChannelsUpdateCall{
        resource: r,
        channel:  Channel{ID: broadcasterID},
    }
}

// BroadcasterID filters the results to the specified broadcaster ID.
func (c *ChannelsListCall) BroadcasterID(ids []string) *ChannelsListCall {
	for _, id := range ids {
		c.opts = append(c.opts, AddQueryParameter("broadcaster_id", id))
	}
	return c
}

// Do executes the request.
func (c *ChannelsListCall) Do(ctx context.Context, opts ...RequestOption) (*ChannelsListResponse, error) {
	res, err := c.resource.client.doRequest(ctx, http.MethodGet, "/channels", nil, append(opts, c.opts...)...)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	data, err := decodeResponse[Channel](res)
	if err != nil {
		return nil, err
	}

	return &ChannelsListResponse{
		Header: res.Header,
		Data:   data.Data,
	}, nil
}

func (c *ChannelsUpdateCall) Do(ctx context.Context, opts ...RequestOption) error {
    // Convert the channel struct to JSON for the request body
    body, err := json.Marshal(c.channel)
    if err != nil {
        return err
    }

    // Execute the PATCH request
    res, err := c.resource.client.doRequest(ctx, http.MethodPatch, "/channels", bytes.NewReader(body), append(opts, c.opts...)...)
    if err != nil {
        return err
    }
    defer res.Body.Close()

    // Handle response here...
    // For example, check if the status code is 200 OK or handle errors

    return nil
}
