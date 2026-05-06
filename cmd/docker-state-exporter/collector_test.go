package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/expfmt"
)

// fakeDocker implements DockerClient for tests.
type fakeDocker struct {
	listResp     []types.Container
	listErr      error
	listCalls    int
	inspectByID  map[string]types.ContainerJSON
	inspectErr   map[string]error
	inspectCalls int
	pingErr      error
}

func (f *fakeDocker) ContainerList(_ context.Context, _ container.ListOptions) ([]types.Container, error) {
	f.listCalls++
	return f.listResp, f.listErr
}

func (f *fakeDocker) ContainerInspect(_ context.Context, id string) (types.ContainerJSON, error) {
	f.inspectCalls++
	if err, ok := f.inspectErr[id]; ok {
		return types.ContainerJSON{}, err
	}
	info, ok := f.inspectByID[id]
	if !ok {
		return types.ContainerJSON{}, errors.New("not found")
	}
	return info, nil
}

func (f *fakeDocker) Ping(_ context.Context) (types.Ping, error) { return types.Ping{}, f.pingErr }
func (f *fakeDocker) Close() error                               { return nil }

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func makeContainer(id, name, image string, state container.ContainerState, health *types.Health, oom bool, restarts int) types.ContainerJSON {
	startedAt := "2024-01-01T10:00:00Z"
	finishedAt := "0001-01-01T00:00:00Z"
	created := "2024-01-01T09:00:00Z"
	return types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{
			ID:           id,
			Name:         "/" + name,
			Image:        image,
			Created:      created,
			RestartCount: restarts,
			State: &types.ContainerState{
				Status:     string(state),
				OOMKilled:  oom,
				StartedAt:  startedAt,
				FinishedAt: finishedAt,
				Health:     health,
			},
		},
		Config: &container.Config{
			Image:    image,
			Hostname: name + "-host",
			Labels:   map[string]string{},
		},
	}
}

func TestCollect_HappyPath_SingleRunningHealthy(t *testing.T) {
	info := makeContainer("c1", "web", "nginx:latest", "running",
		&types.Health{Status: "healthy"}, false, 0)

	docker := &fakeDocker{
		listResp:    []types.Container{{ID: "c1"}},
		inspectByID: map[string]types.ContainerJSON{"c1": info},
	}

	c := newCollector(collectorOptions{
		Client:   docker,
		Logger:   quietLogger(),
		CacheTTL: time.Second,
		Now:      time.Now,
	})

	count, err := testutil.GatherAndCount(registryWith(c),
		"container_state_health_status",
		"container_state_status",
		"container_state_oomkilled",
		"container_state_startedat",
		"container_state_finishedat",
		"container_state_created",
		"container_restartcount",
	)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	expected := len(healthStates) + len(containerStates) + 5
	if count != expected {
		t.Errorf("expected %d series, got %d", expected, count)
	}

	body, err := gatherText(c)
	if err != nil {
		t.Fatalf("gather text: %v", err)
	}
	if !strings.Contains(body, `container_state_health_status{container_hostname="web-host",id="/docker/c1",image="nginx:latest",name="web",status="healthy"} 1`) {
		t.Errorf("missing healthy=1 series:\n%s", body)
	}
	if !strings.Contains(body, `status="unhealthy"} 0`) {
		t.Errorf("missing unhealthy=0 series:\n%s", body)
	}
	if !strings.Contains(body, `container_state_status{container_hostname="web-host",id="/docker/c1",image="nginx:latest",name="web",status="running"} 1`) {
		t.Errorf("missing status=running series:\n%s", body)
	}
}

func TestCollect_NilHealth_EmitsNoneOnly(t *testing.T) {
	info := makeContainer("c1", "web", "nginx", "running", nil, false, 0)

	docker := &fakeDocker{
		listResp:    []types.Container{{ID: "c1"}},
		inspectByID: map[string]types.ContainerJSON{"c1": info},
	}
	c := newCollector(collectorOptions{Client: docker, Logger: quietLogger(), CacheTTL: time.Second})

	body, err := gatherText(c)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if !strings.Contains(body, `container_state_health_status{container_hostname="web-host",id="/docker/c1",image="nginx",name="web",status="none"} 1`) {
		t.Errorf("expected none=1 when Health is nil:\n%s", body)
	}
	if !strings.Contains(body, `status="healthy"} 0`) {
		t.Errorf("expected healthy=0:\n%s", body)
	}
}

func TestCollect_NilConfig_NoPanicAndEmitsBaseLabels(t *testing.T) {
	info := types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{
			ID:      "abc",
			Name:    "/orphan",
			State:   &types.ContainerState{Status: "exited", StartedAt: "0001-01-01T00:00:00Z", FinishedAt: "0001-01-01T00:00:00Z"},
			Created: "0001-01-01T00:00:00Z",
		},
		Config: nil,
	}
	docker := &fakeDocker{
		listResp:    []types.Container{{ID: "abc"}},
		inspectByID: map[string]types.ContainerJSON{"abc": info},
	}
	c := newCollector(collectorOptions{Client: docker, Logger: quietLogger(), CacheTTL: time.Second})

	body, err := gatherText(c)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if !strings.Contains(body, `name="orphan"`) {
		t.Errorf("expected orphan name, got:\n%s", body)
	}
	if !strings.Contains(body, `image=""`) {
		t.Errorf("expected empty image label, got:\n%s", body)
	}
}

func TestCollect_CacheTTL_WithinWindowReusesSnapshot(t *testing.T) {
	info := makeContainer("c1", "web", "nginx", "running", &types.Health{Status: "healthy"}, false, 0)
	docker := &fakeDocker{
		listResp:    []types.Container{{ID: "c1"}},
		inspectByID: map[string]types.ContainerJSON{"c1": info},
	}

	clock := &fakeClock{t: time.Unix(0, 0)}
	c := newCollector(collectorOptions{
		Client:   docker,
		Logger:   quietLogger(),
		CacheTTL: 5 * time.Second,
		Now:      clock.Now,
	})

	if _, err := gatherText(c); err != nil {
		t.Fatalf("first gather: %v", err)
	}
	clock.advance(2 * time.Second)
	if _, err := gatherText(c); err != nil {
		t.Fatalf("second gather: %v", err)
	}

	if docker.listCalls != 1 {
		t.Errorf("expected 1 ContainerList call within TTL window, got %d", docker.listCalls)
	}
}

func TestCollect_CacheTTL_ExpiryRefetches(t *testing.T) {
	info := makeContainer("c1", "web", "nginx", "running", &types.Health{Status: "healthy"}, false, 0)
	docker := &fakeDocker{
		listResp:    []types.Container{{ID: "c1"}},
		inspectByID: map[string]types.ContainerJSON{"c1": info},
	}

	clock := &fakeClock{t: time.Unix(0, 0)}
	c := newCollector(collectorOptions{
		Client:   docker,
		Logger:   quietLogger(),
		CacheTTL: 1 * time.Second,
		Now:      clock.Now,
	})

	if _, err := gatherText(c); err != nil {
		t.Fatalf("first gather: %v", err)
	}
	clock.advance(2 * time.Second)
	if _, err := gatherText(c); err != nil {
		t.Fatalf("second gather: %v", err)
	}

	if docker.listCalls != 2 {
		t.Errorf("expected 2 ContainerList calls after TTL expiry, got %d", docker.listCalls)
	}
}

func TestCollect_MultipleContainersDifferentStates(t *testing.T) {
	web := makeContainer("c1", "web", "nginx", "running", &types.Health{Status: "healthy"}, false, 0)
	worker := makeContainer("c2", "worker", "app", "exited", &types.Health{Status: "unhealthy"}, true, 3)
	idle := makeContainer("c3", "idle", "redis", "paused", nil, false, 0)

	docker := &fakeDocker{
		listResp: []types.Container{{ID: "c1"}, {ID: "c2"}, {ID: "c3"}},
		inspectByID: map[string]types.ContainerJSON{
			"c1": web, "c2": worker, "c3": idle,
		},
	}
	c := newCollector(collectorOptions{Client: docker, Logger: quietLogger(), CacheTTL: time.Second})

	body, err := gatherText(c)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, want := range []string{
		`name="web"`,
		`name="worker"`,
		`name="idle"`,
		`container_state_oomkilled{container_hostname="worker-host",id="/docker/c2",image="app",name="worker"} 1`,
		`container_restartcount{container_hostname="worker-host",id="/docker/c2",image="app",name="worker"} 3`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q:\n%s", want, body)
		}
	}
}

func TestCollect_EmptyList(t *testing.T) {
	docker := &fakeDocker{listResp: nil}
	c := newCollector(collectorOptions{Client: docker, Logger: quietLogger(), CacheTTL: time.Second})

	body, err := gatherText(c)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if strings.Contains(body, "container_state_status") {
		t.Errorf("expected no metric series with empty list, got:\n%s", body)
	}
}

func TestCollect_InspectErrorSkipsContainer(t *testing.T) {
	web := makeContainer("c1", "web", "nginx", "running", &types.Health{Status: "healthy"}, false, 0)
	other := makeContainer("c3", "ok", "redis", "running", &types.Health{Status: "healthy"}, false, 0)

	docker := &fakeDocker{
		listResp: []types.Container{{ID: "c1"}, {ID: "c2"}, {ID: "c3"}},
		inspectByID: map[string]types.ContainerJSON{
			"c1": web,
			"c3": other,
		},
		inspectErr: map[string]error{"c2": errors.New("boom")},
	}
	c := newCollector(collectorOptions{Client: docker, Logger: quietLogger(), CacheTTL: time.Second})

	body, err := gatherText(c)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if !strings.Contains(body, `name="web"`) || !strings.Contains(body, `name="ok"`) {
		t.Errorf("expected the two healthy containers' series to be present:\n%s", body)
	}
	if strings.Contains(body, `id="/docker/c2"`) {
		t.Errorf("did not expect failed container c2 to appear:\n%s", body)
	}
}

// --- helpers ---

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func registryWith(c prometheus.Collector) *prometheus.Registry {
	r := prometheus.NewRegistry()
	r.MustRegister(c)
	return r
}

func gatherText(c prometheus.Collector) (string, error) {
	r := registryWith(c)
	mfs, err := r.Gather()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	enc := expfmt.NewEncoder(&buf, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, mf := range mfs {
		if err := enc.Encode(mf); err != nil {
			return "", err
		}
	}
	return buf.String(), nil
}
