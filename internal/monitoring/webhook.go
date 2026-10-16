package monitoring

import (
	"fmt"
	"strings"
	"time"
)

// Webhook is what Alertmanager POSTs to a webhook receiver (version 4 of
// its payload): one group of alerts.
type Webhook struct {
	Status string  `json:"status"`
	Alerts []Alert `json:"alerts"`
}

// Alert is one alert of a Webhook.
type Alert struct {
	Status      string            `json:"status"` // firing or resolved
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
}

// Text is the group as a Telegram message: one line per alert, such as
// "FIRING critical NodeUnreachable: Node ab1 is unreachable". It is ""
// for a group with no alerts.
func (w Webhook) Text() string {
	var lines []string
	for _, a := range w.Alerts {
		summary := a.Annotations["summary"]
		if summary == "" {
			summary = fmt.Sprint(a.Labels)
		}
		lines = append(lines, fmt.Sprintf("%s %s %s: %s", strings.ToUpper(a.Status), a.Labels["severity"], a.Labels["alertname"], summary))
	}
	if len(lines) == 0 {
		return ""
	}
	return "DARZ\n" + strings.Join(lines, "\n")
}
