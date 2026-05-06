package main

import "github.com/prometheus/client_golang/prometheus"

const namespace = "container_state_"

var (
	healthStatusDesc = descSource{
		name: namespace + "health_status",
		help: "Container health status.",
	}
	statusDesc = descSource{
		name: namespace + "status",
		help: "Container status.",
	}
	oomkilledDesc = descSource{
		name: namespace + "oomkilled",
		help: "Container was killed by OOMKiller.",
	}
	startedatDesc = descSource{
		name: namespace + "startedat",
		help: "Time when the container started (Unix seconds).",
	}
	finishedatDesc = descSource{
		name: namespace + "finishedat",
		help: "Time when the container finished (Unix seconds).",
	}
	createdatDesc = descSource{
		name: namespace + "created",
		help: "Time when the container was created (Unix seconds).",
	}
	restartcountDesc = descSource{
		name: "container_restartcount",
		help: "Number of times the container has been restarted.",
	}
)

var (
	healthStates    = []string{"none", "starting", "healthy", "unhealthy"}
	containerStates = []string{"paused", "restarting", "running", "removing", "dead", "created", "exited"}
)

type descSource struct {
	name string
	help string
}

func (d descSource) Desc(labels prometheus.Labels) *prometheus.Desc {
	return prometheus.NewDesc(d.name, d.help, nil, labels)
}
