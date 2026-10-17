package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"bord/internal/security"
)

type BanRequest struct {
	SubjectType string    `json:"subject_type"`
	Subject     string    `json:"subject"`
	Operations  []string  `json:"operations"`
	ExpiresAt   time.Time `json:"expires_at"`
	Reason      string    `json:"reason"`
}

func ReadCredentialFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", errors.New("cannot read credential file")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("credential file must be a regular file inaccessible to group/others")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("cannot read credential file")
	}
	value := strings.TrimSpace(string(data))
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("invalid credential file")
	}
	return value, nil
}

func ManagementBan(ctx context.Context, client *http.Client, baseURL, credential string, request BanRequest) (string, error) {
	if security.ContainsRawIP(request.Reason) || strings.TrimSpace(request.Reason) == "" {
		return "", errors.New("a non-sensitive reason is required")
	}
	if request.SubjectType != "ip" && request.SubjectType != "prefix" && request.SubjectType != "lookup" {
		return "", errors.New("invalid ban subject type")
	}
	if len(request.Operations) == 0 || !request.ExpiresAt.After(time.Now()) {
		return "", errors.New("ban operations and future expiry are required")
	}
	var response struct {
		ID string `json:"id"`
	}
	if err := managementJSON(ctx, client, http.MethodPost, baseURL+"/admin/v1/bans", credential, request, http.StatusCreated, &response); err != nil {
		return "", err
	}
	if response.ID == "" {
		return "", errors.New("management API returned an empty ban id")
	}
	return response.ID, nil
}

func ManagementUnban(ctx context.Context, client *http.Client, baseURL, credential, id, reason string) error {
	if id == "" || strings.TrimSpace(reason) == "" || security.ContainsRawIP(reason) {
		return errors.New("ban id and non-sensitive reason are required")
	}
	return managementJSON(ctx, client, http.MethodDelete, baseURL+"/admin/v1/bans/"+url.PathEscape(id), credential, map[string]string{"reason": reason}, http.StatusNoContent, nil)
}

func managementJSON(ctx context.Context, client *http.Client, method, endpoint, credential string, body any, expected int, output any) error {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("invalid management URL")
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, parsed.String(), bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("management request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != expected {
		io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return fmt.Errorf("management request returned HTTP %d", response.StatusCode)
	}
	if output == nil {
		io.Copy(io.Discard, response.Body)
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
		return errors.New("invalid management response")
	}
	return nil
}
