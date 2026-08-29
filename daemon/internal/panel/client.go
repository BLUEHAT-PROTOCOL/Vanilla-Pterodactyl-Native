// Package panel implements the daemon -> panel remote API client (panel v1.15 protocol).
package panel

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ptero-native/internal/util"
)

// Client calls the Panel /api/remote/* endpoints.
type Client struct {
	BaseURL  string
	Token    string // the node daemon token secret (also the JWT signing key)
	HTTP     *http.Client
	Insecure bool
}

// New builds a panel client.
func New(baseURL, token string, allowInsecure bool) *Client {
	tr := &http.Transport{}
	if allowInsecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec — opt-in only
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second, Transport: tr},
	}
}

func (c *Client) do(method, path string, body io.Reader, out interface{}) error {
	req, err := http.NewRequest(method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("panel %s %s: %d: %s", method, path, resp.StatusCode, util.TruncateLine(string(data), 500))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("panel %s: decode: %w", path, err)
		}
	}
	return nil
}

// GetServer fetches the full server detail ({settings, process_configuration}).
func (c *Client) GetServer(uuid string) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := c.do(http.MethodGet, "/api/remote/servers/"+uuid, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AllServers lists all servers assigned to this node (paginated, follows meta).
func (c *Client) AllServers() ([]map[string]interface{}, error) {
	var all []map[string]interface{}
	page := 1
	for {
		var out struct {
			Data []map[string]interface{} `json:"data"`
			Meta struct {
				Pagination struct {
					CurrentPage int `json:"current_page"`
					TotalPages  int `json:"total_pages"`
				} `json:"pagination"`
			} `json:"meta"`
		}
		path := fmt.Sprintf("/api/remote/servers?per_page=100&page=%d", page)
		if err := c.do(http.MethodGet, path, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Data...)
		if out.Meta.Pagination.TotalPages <= page || len(out.Data) == 0 {
			break
		}
		page++
	}
	return all, nil
}

// ReportInstall reports install completion (v1.15: single endpoint with successful flag).
func (c *Client) ReportInstall(uuid string, successful bool, reinstall bool) error {
	b, _ := json.Marshal(map[string]interface{}{
		"successful": successful,
		"reinstall":  reinstall,
	})
	return c.do(http.MethodPost, "/api/remote/servers/"+uuid+"/install", strings.NewReader(string(b)), nil)
}

// GetInstallScript fetches the egg install script ({container_image, entrypoint, script}).
func (c *Client) GetInstallScript(uuid string) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := c.do(http.MethodGet, "/api/remote/servers/"+uuid+"/install", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ReportBackup reports backup completion (v1.15: POST /api/remote/backups/{backup}).
func (c *Client) ReportBackup(backup string, successful bool, checksum, checksumType string, size int64) error {
	b, _ := json.Marshal(map[string]interface{}{
		"successful":    successful,
		"checksum":      checksum,
		"checksum_type": checksumType,
		"size":          size,
	})
	return c.do(http.MethodPost, "/api/remote/backups/"+backup, strings.NewReader(string(b)), nil)
}

// ReportRestore reports restore completion (POST /api/remote/backups/{backup}/restore).
func (c *Client) ReportRestore(backup string, successful bool, message string) error {
	if successful {
		b, _ := json.Marshal(map[string]interface{}{"successful": true})
		return c.do(http.MethodPost, "/api/remote/backups/"+backup+"/restore", strings.NewReader(string(b)), nil)
	}
	b, _ := json.Marshal(map[string]interface{}{"successful": false, "error": message})
	return c.do(http.MethodPost, "/api/remote/backups/"+backup+"/restore", strings.NewReader(string(b)), nil)
}

// GetRuntimeMappings fetches the native runtime mapping table from the panel fork.
func (c *Client) GetRuntimeMappings() ([]map[string]interface{}, error) {
	var out struct {
		Data []map[string]interface{} `json:"data"`
	}
	if err := c.do(http.MethodGet, "/api/remote/runtime/mappings", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// DownloadFromPanel fetches a panel-mediated download (egg install assets).
func (c *Client) DownloadFromPanel(token string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/api/remote/servers/download/"+token, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	return c.HTTP.Do(req)
}

// RawGet performs a raw GET (caller closes body).
func (c *Client) RawGet(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	return c.HTTP.Do(req)
}
