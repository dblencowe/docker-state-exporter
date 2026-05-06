package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/prometheus/client_golang/prometheus"
)

type collectorOptions struct {
	Client    DockerClient
	Logger    *slog.Logger
	HostLabel string
	CacheTTL  time.Duration
	Now       func() time.Time
}

type dockerHealthCollector struct {
	mu        sync.Mutex
	client    DockerClient
	logger    *slog.Logger
	hostLabel string
	cacheTTL  time.Duration
	now       func() time.Time

	cache    []types.ContainerJSON
	lastSeen time.Time
}

func newCollector(opts collectorOptions) *dockerHealthCollector {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &dockerHealthCollector{
		client:    opts.Client,
		logger:    opts.Logger,
		hostLabel: opts.HostLabel,
		cacheTTL:  opts.CacheTTL,
		now:       opts.Now,
	}
}

func (c *dockerHealthCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- healthStatusDesc.Desc(nil)
	ch <- statusDesc.Desc(nil)
	ch <- oomkilledDesc.Desc(nil)
	ch <- startedatDesc.Desc(nil)
	ch <- finishedatDesc.Desc(nil)
	ch <- createdatDesc.Desc(nil)
	ch <- restartcountDesc.Desc(nil)
}

func (c *dockerHealthCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.now().Sub(c.lastSeen) >= c.cacheTTL {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.refresh(ctx); err != nil {
			c.logger.Error("docker refresh failed; serving stale cache", "error", err.Error())
		} else {
			c.lastSeen = c.now()
		}
	}

	c.emit(ch)
}

func (c *dockerHealthCollector) refresh(ctx context.Context) error {
	containers, err := c.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return err
	}

	fresh := make([]types.ContainerJSON, 0, len(containers))
	for _, ct := range containers {
		info, err := c.client.ContainerInspect(ctx, ct.ID)
		if err != nil {
			c.logger.Warn("container inspect failed; skipping",
				"container_id", ct.ID,
				"error", err.Error(),
			)
			continue
		}
		fresh = append(fresh, normalize(info))
	}
	c.cache = fresh
	return nil
}

// normalize fills in nil sub-structs so downstream emit code can index without
// guarding every field. Mutates a value receiver so callers see the result.
func normalize(info types.ContainerJSON) types.ContainerJSON {
	if info.Config == nil {
		info.Config = &container.Config{Labels: map[string]string{}}
	}
	if info.Config.Labels == nil {
		info.Config.Labels = map[string]string{}
	}
	if info.ContainerJSONBase != nil && info.State == nil {
		info.State = &types.ContainerState{}
	}
	if info.State != nil && info.State.Health == nil {
		info.State.Health = &types.Health{Status: "none"}
	}
	return info
}

func (c *dockerHealthCollector) emit(ch chan<- prometheus.Metric) {
	for _, info := range c.cache {
		labels := buildLabels(info, c.hostLabel)

		emitStateGauges(ch, healthStatusDesc, healthStates, healthStatus(info), labels)
		emitStateGauges(ch, statusDesc, containerStates, containerStatus(info), labels)

		ch <- prometheus.MustNewConstMetric(oomkilledDesc.Desc(labels), prometheus.GaugeValue, boolToFloat(oomKilled(info)))
		ch <- prometheus.MustNewConstMetric(startedatDesc.Desc(labels), prometheus.GaugeValue, c.parseTimestamp(info, "startedat", startedAt(info)))
		ch <- prometheus.MustNewConstMetric(finishedatDesc.Desc(labels), prometheus.GaugeValue, c.parseTimestamp(info, "finishedat", finishedAt(info)))
		ch <- prometheus.MustNewConstMetric(createdatDesc.Desc(labels), prometheus.GaugeValue, c.parseTimestamp(info, "created", createdAt(info)))
		ch <- prometheus.MustNewConstMetric(restartcountDesc.Desc(labels), prometheus.GaugeValue, float64(restartCount(info)))
	}
}

func emitStateGauges(ch chan<- prometheus.Metric, desc descSource, allStates []string, current string, base prometheus.Labels) {
	for _, state := range allStates {
		labels := copyLabels(base)
		labels["status"] = state
		ch <- prometheus.MustNewConstMetric(desc.Desc(labels), prometheus.GaugeValue, boolToFloat(current == state))
	}
}

func (c *dockerHealthCollector) parseTimestamp(info types.ContainerJSON, field, raw string) float64 {
	if raw == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		c.logger.Warn("timestamp parse failed",
			"container_id", info.ID,
			"field", field,
			"value", raw,
			"error", err.Error(),
		)
		return 0
	}
	return float64(t.Unix())
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func healthStatus(info types.ContainerJSON) string {
	if info.State == nil || info.State.Health == nil {
		return "none"
	}
	return info.State.Health.Status
}

func containerStatus(info types.ContainerJSON) string {
	if info.State == nil {
		return ""
	}
	return info.State.Status
}

func oomKilled(info types.ContainerJSON) bool {
	if info.State == nil {
		return false
	}
	return info.State.OOMKilled
}

func startedAt(info types.ContainerJSON) string {
	if info.State == nil {
		return ""
	}
	return info.State.StartedAt
}

func finishedAt(info types.ContainerJSON) string {
	if info.State == nil {
		return ""
	}
	return info.State.FinishedAt
}

func createdAt(info types.ContainerJSON) string {
	if info.ContainerJSONBase == nil {
		return ""
	}
	return info.Created
}

func restartCount(info types.ContainerJSON) int {
	if info.ContainerJSONBase == nil {
		return 0
	}
	return info.RestartCount
}
