package mcpclient

import "time"

// SetMaxRetries sets the SDK's reconnect limit of a stream, so that a test does not wait
// out the default backoff.
func (c *Client) SetMaxRetries(n int) { c.maxRetries = n }

// SetPauses shortens the pauses of Watch: the first reconnect pause, the longest one, the
// pause after a 401 and how long a connection holds before the pauses start over.
func (c *Client) SetPauses(first, maxPause, unauthorized, stable time.Duration) {
	c.pauses = pauses{first: first, max: maxPause, unauthorized: unauthorized, stable: stable}
}
