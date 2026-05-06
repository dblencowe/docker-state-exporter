package main

import (
	"regexp"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/prometheus/client_golang/prometheus"
)

var labelKeySanitize = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// defaultLabelDeny matches container label keys that should never be emitted
// as Prometheus labels. The two named patterns are Compose-managed metadata
// that change on every rebuild and would otherwise blow up series cardinality.
var defaultLabelDeny = regexp.MustCompile(`config[_-]?hash|oneoff`)

func sanitizeLabelKey(k string) string {
	return labelKeySanitize.ReplaceAllLiteralString(strings.ToLower("container_label_"+k), "_")
}

// buildLabels produces the Prometheus label set for a single container.
// hostLabel is emitted as the "host" label when non-empty; an empty string
// omits it entirely (preserving series identity for users who don't opt in).
func buildLabels(info types.ContainerJSON, hostLabel string) prometheus.Labels {
	labels := prometheus.Labels{}

	if info.Config != nil {
		for k, v := range info.Config.Labels {
			if defaultLabelDeny.MatchString(strings.ToLower(k)) {
				continue
			}
			labels[sanitizeLabelKey(k)] = v
		}
	}

	labels["id"] = "/docker/" + info.ID
	labels["name"] = strings.TrimPrefix(info.Name, "/")

	if info.Config != nil {
		labels["image"] = info.Config.Image
		labels["container_hostname"] = info.Config.Hostname
	} else {
		labels["image"] = ""
		labels["container_hostname"] = ""
	}

	if hostLabel != "" {
		labels["host"] = hostLabel
	}

	return labels
}

func copyLabels(src prometheus.Labels) prometheus.Labels {
	dst := make(prometheus.Labels, len(src)+1)
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
