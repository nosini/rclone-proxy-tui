package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"time"
)

// rcCall posts a JSON request to an rclone rc unix socket.
func rcCall(sock, method string, in, out any) error {
	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
	defer client.CloseIdleConnections()
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	resp, err := client.Post("http://rclone/"+method, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// pendingUploads returns the number of files queued or being uploaded from
// the VFS caches of the rclone server behind sock. Errors count as zero:
// if we can't ask, we can't wait.
func pendingUploads(sock string) int {
	if sock == "" {
		return 0
	}
	var list struct {
		VFSes []string `json:"vfses"`
	}
	if err := rcCall(sock, "vfs/list", map[string]any{}, &list); err != nil {
		return 0
	}
	total := 0
	for _, fs := range list.VFSes {
		var stats struct {
			DiskCache struct {
				UploadsInProgress int `json:"uploadsInProgress"`
				UploadsQueued     int `json:"uploadsQueued"`
			} `json:"diskCache"`
		}
		if err := rcCall(sock, "vfs/stats", map[string]any{"fs": fs}, &stats); err != nil {
			continue
		}
		total += stats.DiskCache.UploadsInProgress + stats.DiskCache.UploadsQueued
	}
	return total
}
