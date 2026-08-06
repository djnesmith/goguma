package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// webhookPayload is the JSON posted to the configured webhook URL.
//
// Only problems are dispatched — overruns, wake failures, undetected jobs,
// and cutouts. A webhook that fires on every successful run would be noise
// that trains the user to ignore it, which defeats the purpose of having one.
type webhookPayload struct {
	Event    string    `json:"event"`
	Job      string    `json:"job,omitempty"`
	Outcome  string    `json:"outcome,omitempty"`
	Message  string    `json:"message"`
	Duration string    `json:"duration,omitempty"`
	At       time.Time `json:"at"`
	Host     string    `json:"host,omitempty"`
}

type webhookSender struct {
	log    *slog.Logger
	client *http.Client
	host   string
}

func newWebhookSender(log *slog.Logger) *webhookSender {
	return &webhookSender{
		log: log,
		// A short timeout on purpose: this runs from the daemon's control
		// path, and a hanging webhook must never delay releasing a sleep hold.
		client: &http.Client{Timeout: 10 * time.Second},
		host:   hostname(),
	}
}

// send posts asynchronously and never blocks the caller. Delivery is
// best-effort: a failed webhook is logged, not retried, because the event it
// describes has already been written to the local event log, which is the
// durable record.
func (w *webhookSender) send(url string, p webhookPayload) {
	if url == "" {
		return
	}
	if p.At.IsZero() {
		p.At = time.Now()
	}
	p.Host = w.host

	go func() {
		body, err := json.Marshal(p)
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			w.log.Warn("webhook request could not be built", "err", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "wakeguard")

		resp, err := w.client.Do(req)
		if err != nil {
			w.log.Warn("webhook delivery failed", "event", p.Event, "err", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			w.log.Warn("webhook rejected", "event", p.Event, "status", resp.StatusCode)
		}
	}()
}

func hostname() string {
	h, err := osHostname()
	if err != nil {
		return ""
	}
	return h
}
