package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"bord/api/generated"
)

const maxResponseBytes = 16 << 20

// APIError is the stable, safe error returned by the public API.
type APIError struct {
	StatusCode int
	Code       generated.ErrorCode
	Message    string
	RequestID  string
	Fields     map[string]string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" {
		return fmt.Sprintf("bord API: %s (request %s)", e.Code, e.RequestID)
	}
	return fmt.Sprintf("bord API: HTTP %d", e.StatusCode)
}

// API wraps the generated transport with typed results, bounded decoding and timeouts.
type API struct {
	raw     generated.ClientInterface
	timeout time.Duration
}

func New(server string, httpClient *http.Client, timeout time.Duration) (*API, error) {
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("invalid server URL")
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	raw, err := generated.NewClient(strings.TrimRight(server, "/"), generated.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("create HTTP client: %w", err)
	}
	return &API{raw: raw, timeout: timeout}, nil
}

// NewWithTransport is intended for adapters and tests which implement the frozen generated contract.
func NewWithTransport(raw generated.ClientInterface, timeout time.Duration) *API {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &API{raw: raw, timeout: timeout}
}

func (c *API) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.timeout)
}

func (c *API) Boards(ctx context.Context) ([]generated.Board, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	resp, err := c.raw.ListBoards(ctx)
	if err != nil {
		return nil, transportError(err)
	}
	var body struct {
		Boards []generated.Board `json:"boards"`
	}
	if err := decodeJSON(resp, http.StatusOK, &body); err != nil {
		return nil, err
	}
	return body.Boards, nil
}

func (c *API) Threads(ctx context.Context, board string, cursor *string, limit int) (generated.ThreadPage, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	params := &generated.ListThreadsParams{Cursor: cursor}
	if limit > 0 {
		params.Limit = &limit
	}
	resp, err := c.raw.ListThreads(ctx, board, params)
	if err != nil {
		return generated.ThreadPage{}, transportError(err)
	}
	var result generated.ThreadPage
	err = decodeJSON(resp, http.StatusOK, &result)
	return result, err
}

func (c *API) Thread(ctx context.Context, id string) (generated.Thread, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	resp, err := c.raw.GetThread(ctx, id)
	if err != nil {
		return generated.Thread{}, transportError(err)
	}
	var result generated.Thread
	err = decodeJSON(resp, http.StatusOK, &result)
	return result, err
}

func (c *API) Posts(ctx context.Context, threadID string, cursor *string, limit int) (generated.PostPage, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	params := &generated.ListPostsParams{Cursor: cursor}
	if limit > 0 {
		params.Limit = &limit
	}
	resp, err := c.raw.ListPosts(ctx, threadID, params)
	if err != nil {
		return generated.PostPage{}, transportError(err)
	}
	var result generated.PostPage
	err = decodeJSON(resp, http.StatusOK, &result)
	return result, err
}

func (c *API) CreateThread(ctx context.Context, board, idempotencyKey string, body generated.CreateThread) (generated.CreateResult, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	resp, err := c.raw.CreateThread(ctx, board, &generated.CreateThreadParams{IdempotencyKey: idempotencyKey}, body)
	if err != nil {
		return generated.CreateResult{}, transportError(err)
	}
	var result generated.CreateResult
	err = decodeJSON(resp, http.StatusCreated, &result)
	return result, err
}

func (c *API) CreatePost(ctx context.Context, threadID, idempotencyKey string, body generated.CreatePost) (generated.CreateResult, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	resp, err := c.raw.CreatePost(ctx, threadID, &generated.CreatePostParams{IdempotencyKey: idempotencyKey}, body)
	if err != nil {
		return generated.CreateResult{}, transportError(err)
	}
	var result generated.CreateResult
	err = decodeJSON(resp, http.StatusCreated, &result)
	return result, err
}

func (c *API) DeletePost(ctx context.Context, postID, deleteToken string) error {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	resp, err := c.raw.DeletePost(ctx, postID, generated.DeletePost{DeleteToken: deleteToken})
	if err != nil {
		return transportError(err)
	}
	return decodeEmpty(resp, http.StatusNoContent)
}

func (c *API) Report(ctx context.Context, postID string, report generated.CreateReport) error {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	resp, err := c.raw.ReportPost(ctx, postID, report)
	if err != nil {
		return transportError(err)
	}
	return decodeEmpty(resp, http.StatusAccepted)
}

func (c *API) UploadAttachment(ctx context.Context, mediaType string, r io.Reader) (generated.Attachment, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	resp, err := c.raw.UploadAttachmentWithBody(ctx, mediaType, r)
	if err != nil {
		return generated.Attachment{}, transportError(err)
	}
	var result generated.Attachment
	err = decodeJSON(resp, http.StatusCreated, &result)
	return result, err
}

// Attachment returns a bounded image body. Callers should verify it against metadata before use.
func (c *API) Attachment(ctx context.Context, id string) ([]byte, string, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	resp, err := c.raw.GetAttachment(ctx, id)
	if err != nil {
		return nil, "", transportError(err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", decodeAPIError(resp)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read attachment response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, "", errors.New("attachment response exceeds client limit")
	}
	return body, resp.Header.Get("Content-Type"), nil
}

func decodeJSON(resp *http.Response, want int, dst any) error {
	if resp == nil {
		return errors.New("empty HTTP response")
	}
	if resp.StatusCode != want {
		return decodeAPIError(resp)
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes))
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decode API response: %w", err)
	}
	return nil
}

func decodeEmpty(resp *http.Response, want int) error {
	if resp == nil {
		return errors.New("empty HTTP response")
	}
	if resp.StatusCode != want {
		return decodeAPIError(resp)
	}
	resp.Body.Close()
	return nil
}

func decodeAPIError(resp *http.Response) error {
	if resp == nil {
		return errors.New("empty HTTP response")
	}
	defer resp.Body.Close()
	result := &APIError{StatusCode: resp.StatusCode}
	var wire generated.Error
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&wire); err == nil {
		result.Code, result.Message, result.RequestID = wire.Code, wire.Message, wire.RequestId
		if wire.Fields != nil {
			result.Fields = *wire.Fields
		}
		if wire.RetryAfter != nil {
			result.RetryAfter = time.Duration(*wire.RetryAfter) * time.Second
		}
	}
	if result.RetryAfter == 0 {
		if delay, err := time.ParseDuration(resp.Header.Get("Retry-After") + "s"); err == nil {
			result.RetryAfter = delay
		}
	}
	return result
}

func transportError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("HTTP request failed: %w", err)
}
