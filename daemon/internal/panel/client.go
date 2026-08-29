// Package panel implements the daemon -> panel remote API client.
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
	Token    string // full daemon token "<id>.<secret>"
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

// ServerDetail is the full server configuration from the panel.
// Reuse server.ServerConfig via a local alias to avoid import cycles.
type ServerDetail = map[string]interface{}

// GetServer fetches the server detail from the panel.
func (c *Client) GetServer(uuid string) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := c.do(http.MethodGet, "/api/remote/servers/"+uuid, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetServersBulk fetches multiple servers by internal panel ids.
func (c *Client) GetServersBulk(ids []int64) ([]map[string]interface{}, error) {
	var sb strings.Builder
	sb.WriteString("/api/remote/servers?")
	for i, id := range ids {
		if i > 0 {
			sb.WriteString("&")
		}
		sb.WriteString(fmt.Sprintf("ids[]=%d", id))
	}
	var out []map[string]interface{}
	if err := c.do(http.MethodGet, sb.String(), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// InstallSuccess reports a successful install.
func (c *Client) InstallSuccess(uuid string) error {
	return c.do(http.MethodPost, "/api/remote/servers/"+uuid+"/install/success", strings.NewReader("{}"), nil)
}

// InstallFailed reports a failed install.
func (c *Client) InstallFailed(uuid, message string) error {
	b, _ := json.Marshal(map[string]string{"message": message})
	return c.do(http.MethodPost, "/api/remote/servers/"+uuid+"/install/failed", strings.NewReader(string(b)), nil)
}

// BackupCompleted reports backup completion.
func (c *Client) BackupCompleted(uuid, backup string, successful bool, checksum, checksumType string, size int64) error {
	b, _ := json.Marshal(map[string]interface{}{
		"successful":    successful,
		"checksum":      checksum,
		"checksum_type": checksumType,
		"size":          size,
	})
	return c.do(http.MethodPost, "/api/remote/servers/"+uuid+"/backups/"+backup, strings.NewReader(string(b)), nil)
}

// RestoreCompleted reports restore completion.
func (c *Client) RestoreCompleted(uuid, backup string, successful bool, message string) error {
	path := "/api/remote/servers/" + uuid + "/backups/" + backup + "/restores"
	if successful {
		return c.do(http.MethodPost, path, strings.NewReader(`{"successful":true}`), nil)
	}
	b, _ := json.Marshal(map[string]string{"message": message})
	return c.do(http.MethodPost, path+"/failed", strings.NewReader(string(b)), nil)
}

// GetInstallScript fetches the install script for a server.
func (c *Client) GetInstallScript(uuid string) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := c.do(http.MethodGet, "/api/remote/servers/"+uuid+"/install", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
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

// PostActivity posts activity events.
func (c *Client) PostActivity(events []map[string]interface{}) error {
	b, _ := json.Marshal(events)
	return c.do(http.MethodPost, "/api/remote/servers/activity", strings.NewReader(string(b)), nil)
}

// RawGet performs a raw GET returning the response (caller closes body).
func (c *Client) RawGet(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	return c.HTTP.Do(req)
}

// AllServers fetches every server assigned to this node.
// Tries the bulk endpoint first, then falls back to paginated discovery.
func (c *Client) AllServers() ([]map[string]interface{}, error) {
	var out []map[string]interface{}
	// Attempt 1: node-wide sync endpoint present in panel (remote/servers with node filter
	// is not public; wings uses ids[]= from its local list). We support the daemon-side
	// convention: panel exposes GET /api/remote/servers/list?node_token=1 returning all.
	err := c.doList("/api/remote/servers/list", &out)
	if err == nil && out != nil {
		return out, nil
	}
	// Attempt 2: paginated application-style remote listing (used by native fork patch)
	err = c.doList("/api/remote/servers?page=1&per_page=1000", &out)
	if err == nil {
		return out, nil
	}
	return nil, err
}

func (c *Client) doList(path string, out *[]map[string]interface{}) error {
	resp, err := c.RawGet(path)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("panel list %s: %d", path, resp.StatusCode)
	}
	var body struct {
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}
	*out = body.Data
	return nil
}
