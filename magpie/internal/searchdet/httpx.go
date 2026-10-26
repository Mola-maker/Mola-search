// The engine's HTTP plumbing: one client, honest timeouts, a body cap and
// polite headers, shared by every channel and page fetch.
package searchdet

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// ua is what the engine says it is: a browser-shaped agent, since several
// keyless endpoints answer HTML meant for one.
const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// maxBody caps what one fetch reads (2 MiB): a search page is far smaller,
// and a runaway reply must not eat the engine's memory.
const maxBody = 2 << 20

var client = &http.Client{
	Timeout: 15 * time.Second,
}

// fetchGet reads a URL as text (Content-Type permitting) with the shared
// client, body cap and UA.
func fetchGet(ctx context.Context, url string, accept string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	return doText(req)
}

// fetchJSON reads a URL into out as JSON.
func fetchJSON(ctx context.Context, url string, header map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return errors.New("HTTP " + resp.Status + " " + string(b))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out)
}

// postJSON sends one JSON body and reads a JSON reply.
func postJSON(ctx context.Context, url string, header map[string]string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return errors.New("HTTP " + resp.Status + " " + string(b))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out)
}

func doText(req *http.Request) (string, error) {
	b, err := doBytes(req)
	return string(b), err
}

// doBytes runs a request and reads its body, capped, status-checked and
// content-type-checked for text.
func doBytes(req *http.Request) ([]byte, error) {
	return doBytesWith(req, func(code int) bool { return code == http.StatusOK })
}

// doBytesAccepted is doBytes for a transport that acknowledges with 202 as
// well as answering 200: an MCP notification is answered 202 with no body.
func doBytesAccepted(req *http.Request) ([]byte, error) {
	return doBytesWith(req, func(code int) bool {
		return code == http.StatusOK || code == http.StatusAccepted
	})
}

func doBytesWith(req *http.Request, accepted func(int) bool) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if !accepted(resp.StatusCode) {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, errors.New("HTTP " + resp.Status + " " + string(b))
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(ct, "text/") && !strings.Contains(ct, "html") && !strings.Contains(ct, "xml") && !strings.Contains(ct, "json") {
		return nil, errors.New("not a text body: " + ct)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

// httpNewRequest builds a request with a string body (postRaw's helper).
func httpNewRequest(ctx context.Context, method, u, body string) (*http.Request, error) {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	return http.NewRequestWithContext(ctx, method, u, r)
}

// httpDeadline is the per-channel budget every channel cuts its context to.
func httpDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	return channelDeadline(ctx, 0)
}

// channelDeadline is httpDeadline with a caller's setting: <= 0 keeps the
// engine's 12 s default.
func channelDeadline(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		d = 12 * time.Second
	}
	return context.WithTimeout(ctx, d)
}
