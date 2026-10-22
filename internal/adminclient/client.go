package adminclient

import (
	"bytes"
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

type Session = generated.AdminSession
type Report = generated.Report
type PostContext = generated.AdminPostContext
type Ban = generated.Ban
type BanRecord = generated.AdminBan
type AuditEntry = generated.AdminAuditEntry
type Board = generated.AdminBoard

type CreateBoardRequest struct {
	Slug   string
	Title  string
	Reason string
}

type Page[T any] struct {
	Items      []T
	NextCursor *string
}

type BanRequest struct {
	SubjectType string
	Subject     string
	Operations  []string
	ExpiresAt   time.Time
	Reason      string
}

type PostBanRequest struct {
	Operations []string
	ExpiresAt  time.Time
	Reason     string
}

type Error struct {
	StatusCode int
	Code       string
	RequestID  string
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("bord management API: %s (request %s)", e.Code, e.RequestID)
	}
	return fmt.Sprintf("bord management API: HTTP %d", e.StatusCode)
}

type Client struct {
	raw     generated.ClientInterface
	timeout time.Duration
}

func New(server, credential string, httpClient *http.Client, timeout time.Duration) (*Client, error) {
	parsed, err := url.Parse(server)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid management URL")
	}
	if strings.TrimSpace(credential) == "" || strings.TrimSpace(credential) != credential {
		return nil, errors.New("invalid management credential")
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	raw, err := generated.NewClient(strings.TrimRight(server, "/"), generated.WithHTTPClient(httpClient), generated.WithRequestEditorFn(func(_ context.Context, request *http.Request) error {
		request.Header.Set("Authorization", "Bearer "+credential)
		return nil
	}))
	if err != nil {
		return nil, errors.New("create management client")
	}
	return &Client{raw: raw, timeout: timeout}, nil
}

func (c *Client) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.timeout)
}

func (c *Client) Session(ctx context.Context) (Session, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	response, err := c.raw.AdminGetSession(ctx)
	var result Session
	return result, c.decode(response, err, http.StatusOK, &result)
}

func (c *Client) Reports(ctx context.Context, cursor *string, limit int) (Page[Report], error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	params := &generated.AdminListReportsParams{Cursor: cursor}
	if limit > 0 {
		params.Limit = &limit
	}
	response, err := c.raw.AdminListReports(ctx, params)
	var wire struct {
		Reports    []Report `json:"reports"`
		NextCursor *string  `json:"next_cursor,omitempty"`
	}
	if err := c.decode(response, err, http.StatusOK, &wire); err != nil {
		return Page[Report]{}, err
	}
	return Page[Report]{Items: wire.Reports, NextCursor: wire.NextCursor}, nil
}

func (c *Client) Boards(ctx context.Context, state string, cursor *string, limit int) (Page[Board], error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	stateValue := generated.AdminListBoardsParamsState(state)
	params := &generated.AdminListBoardsParams{Cursor: cursor, State: &stateValue}
	if limit > 0 {
		params.Limit = &limit
	}
	response, err := c.raw.AdminListBoards(ctx, params)
	var wire generated.AdminBoardPage
	if err := c.decode(response, err, http.StatusOK, &wire); err != nil {
		return Page[Board]{}, err
	}
	return Page[Board]{Items: wire.Boards, NextCursor: wire.NextCursor}, nil
}

func (c *Client) CreateBoard(ctx context.Context, request CreateBoardRequest) (Board, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	response, err := c.raw.AdminCreateBoard(ctx, generated.AdminCreateBoard{Slug: request.Slug, Title: request.Title, Reason: request.Reason})
	var result Board
	return result, c.decode(response, err, http.StatusCreated, &result)
}

func (c *Client) ArchiveBoard(ctx context.Context, boardID, reason string) (Board, error) {
	return c.setBoardState(ctx, boardID, reason, false)
}

func (c *Client) RestoreBoard(ctx context.Context, boardID, reason string) (Board, error) {
	return c.setBoardState(ctx, boardID, reason, true)
}

func (c *Client) setBoardState(ctx context.Context, boardID, reason string, enabled bool) (Board, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	var response *http.Response
	var err error
	if enabled {
		response, err = c.raw.AdminRestoreBoard(ctx, boardID, generated.Reason{Reason: reason})
	} else {
		response, err = c.raw.AdminArchiveBoard(ctx, boardID, generated.Reason{Reason: reason})
	}
	var result Board
	return result, c.decode(response, err, http.StatusOK, &result)
}

func (c *Client) PostContext(ctx context.Context, postID string, cursor *string, limit int) (PostContext, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	params := &generated.AdminGetPostContextParams{Cursor: cursor}
	if limit > 0 {
		params.Limit = &limit
	}
	response, err := c.raw.AdminGetPostContext(ctx, postID, params)
	var result PostContext
	return result, c.decode(response, err, http.StatusOK, &result)
}

func (c *Client) Attachment(ctx context.Context, id string) ([]byte, string, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	response, err := c.raw.AdminGetAttachment(ctx, id)
	if err != nil {
		return nil, "", errors.New("management transport unavailable")
	}
	if response == nil {
		return nil, "", errors.New("empty management response")
	}
	if response.StatusCode != http.StatusOK {
		return nil, "", decodeError(response)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return nil, "", errors.New("invalid attachment response")
	}
	return body, response.Header.Get("Content-Type"), nil
}

func (c *Client) Bans(ctx context.Context, state string, cursor *string, limit int) (Page[BanRecord], error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	stateValue := generated.AdminListBansParamsState(state)
	params := &generated.AdminListBansParams{Cursor: cursor, State: &stateValue}
	if limit > 0 {
		params.Limit = &limit
	}
	response, err := c.raw.AdminListBans(ctx, params)
	var wire generated.AdminBanPage
	if err := c.decode(response, err, http.StatusOK, &wire); err != nil {
		return Page[BanRecord]{}, err
	}
	return Page[BanRecord]{Items: wire.Bans, NextCursor: wire.NextCursor}, nil
}

func (c *Client) Audit(ctx context.Context, cursor *string, limit int) (Page[AuditEntry], error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	params := &generated.AdminListAuditParams{Cursor: cursor}
	if limit > 0 {
		params.Limit = &limit
	}
	response, err := c.raw.AdminListAudit(ctx, params)
	var wire generated.AdminAuditPage
	if err := c.decode(response, err, http.StatusOK, &wire); err != nil {
		return Page[AuditEntry]{}, err
	}
	return Page[AuditEntry]{Items: wire.Entries, NextCursor: wire.NextCursor}, nil
}

func (c *Client) SetVisibility(ctx context.Context, postID string, visible bool, reason string) error {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	response, err := c.raw.AdminSetPostVisibility(ctx, postID, generated.SetVisibility{Visible: visible, Reason: reason})
	return c.decodeEmpty(response, err, http.StatusNoContent)
}

func (c *Client) CloseThread(ctx context.Context, threadID, reason string) error {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	response, err := c.raw.AdminCloseThread(ctx, threadID, generated.Reason{Reason: reason})
	return c.decodeEmpty(response, err, http.StatusNoContent)
}

func (c *Client) CreateBan(ctx context.Context, request BanRequest) (Ban, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	operations := append([]string(nil), request.Operations...)
	response, err := c.raw.AdminCreateBan(ctx, generated.CreateBan{SubjectType: generated.CreateBanSubjectType(request.SubjectType), Subject: request.Subject, Operations: operations, ExpiresAt: request.ExpiresAt, Reason: request.Reason})
	var result Ban
	return result, c.decode(response, err, http.StatusCreated, &result)
}

func (c *Client) CreatePostBan(ctx context.Context, postID string, request PostBanRequest) (Ban, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	operations := make([]generated.CreatePostBanOperations, len(request.Operations))
	for index := range request.Operations {
		operations[index] = generated.CreatePostBanOperations(request.Operations[index])
	}
	response, err := c.raw.AdminCreatePostBan(ctx, postID, generated.CreatePostBan{Operations: operations, ExpiresAt: request.ExpiresAt, Reason: request.Reason})
	var result Ban
	return result, c.decode(response, err, http.StatusCreated, &result)
}

func (c *Client) RevokeBan(ctx context.Context, banID, reason string) error {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	response, err := c.raw.AdminRevokeBan(ctx, banID, generated.Reason{Reason: reason})
	return c.decodeEmpty(response, err, http.StatusNoContent)
}

func (c *Client) RevealIP(ctx context.Context, postID, reason string) (string, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	response, err := c.raw.AdminRevealPostIP(ctx, postID, generated.Reason{Reason: reason})
	var wire struct {
		IP string `json:"ip"`
	}
	if err := c.decode(response, err, http.StatusOK, &wire); err != nil {
		return "", err
	}
	return wire.IP, nil
}

func (c *Client) decode(response *http.Response, transportErr error, status int, target any) error {
	if transportErr != nil {
		return errors.New("management transport unavailable")
	}
	if response == nil {
		return errors.New("empty management response")
	}
	if response.StatusCode != status {
		return decodeError(response)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return errors.New("invalid management response")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid management response")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("invalid management response")
	}
	return nil
}

func (c *Client) decodeEmpty(response *http.Response, transportErr error, status int) error {
	if transportErr != nil {
		return errors.New("management transport unavailable")
	}
	if response == nil {
		return errors.New("empty management response")
	}
	if response.StatusCode != status {
		return decodeError(response)
	}
	response.Body.Close()
	return nil
}

func decodeError(response *http.Response) error {
	defer response.Body.Close()
	result := &Error{StatusCode: response.StatusCode}
	var wire generated.Error
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&wire); err == nil {
		result.Code = string(wire.Code)
		result.RequestID = wire.RequestId
		if wire.RetryAfter != nil {
			result.RetryAfter = time.Duration(*wire.RetryAfter) * time.Second
		}
	}
	return result
}
