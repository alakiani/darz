package coordinator

import (
	"errors"
	"math"
	"net/http"
	"slices"
	"time"

	"github.com/alakiani/ways/internal/protocol"
	"github.com/alakiani/ways/internal/store"
)

// DefaultRefillDays is how long a Refill lasts unless an Admin says
// otherwise (ADR 0010).
const DefaultRefillDays = 30

// Bounds on what Admins give at once.
const (
	maxRefills    = 100
	maxRefillDays = 3650
)

// quotaSettingOf checks a Quota and running-out setting from a request.
// An empty running_out is block when allowEmpty is set, as on an Invite.
func quotaSettingOf(quota *uint64, ro protocol.RunningOut, rate uint64, allowEmpty bool) (store.QuotaSetting, string) {
	q := store.QuotaSetting{Quota: quota}
	if ro == "" && allowEmpty {
		ro = protocol.RunningOutBlock
	}
	switch {
	case quota != nil && *quota > math.MaxInt64:
		return q, "quota is too large"
	case ro == protocol.RunningOutBlock && rate == 0:
	case ro == protocol.RunningOutSlow && rate > 0 && rate <= math.MaxInt64:
		q.SlowRate = rate
	default:
		return q, "running_out is block, or slow with a rate in bytes per second"
	}
	return q, ""
}

// refillTTL checks a number of days a Refill lasts; 0 is the default.
func refillTTL(days int) (time.Duration, bool) {
	if days == 0 {
		days = DefaultRefillDays
	}
	return time.Duration(days) * 24 * time.Hour, days > 0 && days <= maxRefillDays
}

// Every remedy takes effect on the next snapshot, so each one reconciles
// (ADR 0010).

func (c *Coordinator) adminTopUp(w http.ResponseWriter, r *http.Request) {
	a, ok := c.target(w, r)
	if !ok {
		return
	}
	var req protocol.TopUp
	if !decode(w, r, &req) {
		return
	}
	if req.Bytes == 0 || req.Bytes > math.MaxInt64 {
		writeError(w, http.StatusBadRequest, "a Top-up is a positive number of bytes")
		return
	}
	// Allowed on an unlimited Quota too: it counts if a Quota is set later
	// in the period.
	c.afterUserChange(w, r, a.ID, c.store.AddRemedy(r.Context(), a.ID, store.TopUp, req.Bytes))
}

func (c *Coordinator) adminReset(w http.ResponseWriter, r *http.Request) {
	a, ok := c.target(w, r)
	if !ok {
		return
	}
	// Reset now is a Refill given and redeemed at once.
	_, err := c.store.ResetQuota(r.Context(), a.ID)
	if errors.Is(err, store.ErrUnlimited) {
		writeError(w, http.StatusConflict, "an unlimited Quota takes no Reset")
		return
	}
	c.afterUserChange(w, r, a.ID, err)
}

func (c *Coordinator) adminGiveRefills(w http.ResponseWriter, r *http.Request) {
	a, ok := c.target(w, r)
	if !ok {
		return
	}
	var req protocol.GiveRefills
	if !decode(w, r, &req) {
		return
	}
	ttl, ok := refillTTL(req.Days)
	if req.Count < 1 || req.Count > maxRefills || !ok {
		writeError(w, http.StatusBadRequest, "count must be 1 to 100, and days 1 to 3650")
		return
	}
	c.afterUserChange(w, r, a.ID, c.store.GiveRefills(r.Context(), a.ID, req.Count, ttl))
}

func (c *Coordinator) myUsage(w http.ResponseWriter, r *http.Request) {
	u, err := c.usageOf(r, callerFrom(r).account)
	if err != nil {
		c.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (c *Coordinator) redeemRefill(w http.ResponseWriter, r *http.Request) {
	acct := callerFrom(r).account
	_, err := c.store.RedeemRefill(r.Context(), acct.ID, r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such Refill of yours, or it has expired")
		return
	case errors.Is(err, store.ErrUnlimited):
		writeError(w, http.StatusConflict, "your Quota is unlimited, so a Refill has nothing to add")
		return
	case err != nil:
		c.internalError(w, err)
		return
	}
	if err := c.Reconcile(r.Context()); err != nil {
		c.internalError(w, err)
		return
	}
	c.myUsage(w, r)
}

// usageOf is a User's view of their Usage and Quota.
func (c *Coordinator) usageOf(r *http.Request, acct store.Account) (protocol.MyUsage, error) {
	ctx := r.Context()
	q, err := c.store.QuotaStanding(ctx, acct.ID)
	if err != nil {
		return protocol.MyUsage{}, err
	}
	history, err := c.store.UsageHistory(ctx, acct.ID)
	if err != nil {
		return protocol.MyUsage{}, err
	}
	remedies, err := c.store.Remedies(ctx, acct.ID)
	if err != nil {
		return protocol.MyUsage{}, err
	}
	refills, err := c.store.HeldRefills(ctx, acct.ID)
	if err != nil {
		return protocol.MyUsage{}, err
	}
	u := protocol.MyUsage{Quota: acct.Quota, RunningOut: protocol.RunningOutBlock, RenewsAt: q.Period.End,
		History: []protocol.PeriodUsage{}, Refills: []protocol.Refill{}}
	if acct.SlowRate > 0 {
		u.RunningOut, u.Rate = protocol.RunningOutSlow, acct.SlowRate
	}
	if left, limited := q.Left(); limited {
		adds, _ := q.ResetAdds()
		u.Left, u.RefillAdds = &left, &adds
	}
	for _, e := range slices.Backward(history) {
		p := protocol.PeriodUsage{Start: e.Start, End: e.End, Bytes: e.Bytes, Remedies: []protocol.Remedy{},
			Detailed: e.Detailed, Revoked: e.Revoked}
		for _, d := range e.Devices {
			p.Devices = append(p.Devices, protocol.DevicePeriodUsage{ID: d.ID, Name: d.Name, Bytes: d.Bytes})
		}
		for _, rem := range remedies {
			if rem.PeriodStart.Equal(e.Start) {
				p.Remedies = append(p.Remedies, protocol.Remedy{Kind: string(rem.Kind), Bytes: rem.Bytes, At: rem.At})
			}
		}
		if e.Current {
			u.Current = p
		} else {
			u.History = append(u.History, p)
		}
	}
	for _, rf := range refills {
		u.Refills = append(u.Refills, protocol.Refill{ID: rf.ID, ExpiresAt: rf.ExpiresAt})
	}
	return u, nil
}
