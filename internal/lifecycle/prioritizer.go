package lifecycle

import (
	"fmt"
	"sort"
	"time"
)

// NodeWithUptime pairs a discovered node with its uptime.
type NodeWithUptime struct {
	Name     string
	InstanceID string
	Uptime   time.Duration
}

// NodePrioritizer determines the order in which nodes are processed within a pool.
type NodePrioritizer interface {
	// Sort reorders nodes in-place by priority.
	Sort(nodes []NodeWithUptime)
	// Name returns the prioritizer name (for config validation and logging).
	Name() string
}

// ── Built-in prioritizers ────────────────────────────────────────

// highestUptime prioritizes nodes with the most uptime first.
type highestUptime struct{}

func (highestUptime) Name() string { return "highest-uptime" }
func (highestUptime) Sort(nodes []NodeWithUptime) {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Uptime > nodes[j].Uptime
	})
}

// lowestUptime prioritizes nodes with the least uptime first.
type lowestUptime struct{}

func (lowestUptime) Name() string { return "lowest-uptime" }
func (lowestUptime) Sort(nodes []NodeWithUptime) {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Uptime < nodes[j].Uptime
	})
}

// noPriority keeps discovery order.
type noPriority struct{}

func (noPriority) Name() string { return "none" }
func (noPriority) Sort(nodes []NodeWithUptime) {}

// ── Registry ─────────────────────────────────────────────────────

var prioritizers = map[string]NodePrioritizer{
	"highest-uptime": highestUptime{},
	"lowest-uptime":  lowestUptime{},
	"none":           noPriority{},
}

// RegisterPrioritizer adds a custom prioritizer (extensible).
func RegisterPrioritizer(p NodePrioritizer) {
	prioritizers[p.Name()] = p
}

// GetPrioritizer returns the prioritizer for the given name.
func GetPrioritizer(name string) (NodePrioritizer, error) {
	if name == "" {
		return highestUptime{}, nil // default
	}
	p, ok := prioritizers[name]
	if !ok {
		return nil, fmt.Errorf("unknown nodePriority %q", name)
	}
	return p, nil
}
