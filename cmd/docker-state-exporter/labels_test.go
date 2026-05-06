package main

import (
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
)

func TestSanitizeLabelKey(t *testing.T) {
	cases := map[string]string{
		"app":                "container_label_app",
		"App.Version":        "container_label_app_version",
		"com.docker.compose": "container_label_com_docker_compose",
		"trailing-dash-":     "container_label_trailing_dash_",
		"weird/slash\\back":  "container_label_weird_slash_back",
		"UPPERCASE":          "container_label_uppercase",
	}
	for in, want := range cases {
		got := sanitizeLabelKey(in)
		if got != want {
			t.Errorf("sanitizeLabelKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildLabels_DenylistFiltersComposeHash(t *testing.T) {
	info := types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{ID: "abc", Name: "/svc"},
		Config: &container.Config{
			Image:    "nginx:latest",
			Hostname: "svc-host",
			Labels: map[string]string{
				"com.docker.compose.config-hash": "deadbeef",
				"com.docker.compose.oneoff":      "False",
				"app":                            "demo",
			},
		},
	}

	got := buildLabels(info, "")

	if _, denied := got["container_label_com_docker_compose_config_hash"]; denied {
		t.Errorf("config-hash label should be denied, got %v", got)
	}
	if _, denied := got["container_label_com_docker_compose_oneoff"]; denied {
		t.Errorf("oneoff label should be denied, got %v", got)
	}
	if got["container_label_app"] != "demo" {
		t.Errorf("expected app label, got %v", got)
	}
	if got["container_hostname"] != "svc-host" {
		t.Errorf("expected container_hostname=svc-host, got %q", got["container_hostname"])
	}
	if _, present := got["host"]; present {
		t.Errorf("host label should be omitted when hostLabel is empty")
	}
	if got["id"] != "/docker/abc" {
		t.Errorf("expected id=/docker/abc, got %q", got["id"])
	}
	if got["name"] != "svc" {
		t.Errorf("expected name=svc (slash trimmed), got %q", got["name"])
	}
	if got["image"] != "nginx:latest" {
		t.Errorf("expected image=nginx:latest, got %q", got["image"])
	}
}

func TestBuildLabels_NilConfig(t *testing.T) {
	info := types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{ID: "x", Name: "/y"},
		Config:            nil,
	}
	got := buildLabels(info, "")
	if got["id"] != "/docker/x" || got["name"] != "y" {
		t.Errorf("id/name not populated when Config is nil: %v", got)
	}
	if got["image"] != "" || got["container_hostname"] != "" {
		t.Errorf("image/hostname should be empty strings when Config is nil: %v", got)
	}
}

func TestBuildLabels_HostLabelOptIn(t *testing.T) {
	info := types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{ID: "x", Name: "/y"},
		Config:            &container.Config{Hostname: "h"},
	}
	got := buildLabels(info, "node-1")
	if got["host"] != "node-1" {
		t.Errorf("expected host=node-1 when opted in, got %q", got["host"])
	}
}
