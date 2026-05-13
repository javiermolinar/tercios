package scenario

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/javiermolinar/tercios/internal/typedvalue"
)

type EdgeKind string

const (
	EdgeKindClientServer     EdgeKind = "client_server"
	EdgeKindProducerConsumer EdgeKind = "producer_consumer"
	EdgeKindInternal         EdgeKind = "internal"
	EdgeKindClientDatabase   EdgeKind = "client_database"
)

type ValueType = typedvalue.ValueType

const (
	ValueTypeString ValueType = typedvalue.ValueTypeString
	ValueTypeInt    ValueType = typedvalue.ValueTypeInt
	ValueTypeFloat  ValueType = typedvalue.ValueTypeFloat
	ValueTypeBool   ValueType = typedvalue.ValueTypeBool

	ValueTypeStringArray ValueType = typedvalue.ValueTypeStringArray
	ValueTypeIntArray    ValueType = typedvalue.ValueTypeIntArray
	ValueTypeFloatArray  ValueType = typedvalue.ValueTypeFloatArray
	ValueTypeBoolArray   ValueType = typedvalue.ValueTypeBoolArray
)

type TypedValue = typedvalue.TypedValue

type ServiceConfig struct {
	Resource map[string]TypedValue `json:"resource"`
}

type NodeConfig struct {
	Service  string `json:"service"`
	SpanName string `json:"span_name"`
}

type EventConfig struct {
	Name       string                `json:"name"`
	Attributes map[string]TypedValue `json:"attributes,omitempty"`
}

type LinkConfig struct {
	Node       string                `json:"node"`
	Attributes map[string]TypedValue `json:"attributes,omitempty"`
}

type EdgeConfig struct {
	From string   `json:"from"`
	To   string   `json:"to"`
	Kind EdgeKind `json:"kind"`
	// Repeat: how many times this edge fires sequentially.
	Repeat int `json:"repeat"`
	// DurationMs: the edge's own work time. Full span duration is
	// DurationMs + subtreeDuration[To] so the span contains its subtree.
	DurationMs int64 `json:"duration_ms"`
	// NetworkLatencyMs: symmetric one-way network gap. The target-side
	// span of a pair edge is inset by this amount on both sides of the
	// source-side span. Must be 0 for Internal edges. Requires
	// 2*NetworkLatencyMs < DurationMs.
	NetworkLatencyMs int64                 `json:"network_latency_ms,omitempty"`
	SpanAttributes   map[string]TypedValue `json:"span_attributes,omitempty"`
	SpanEvents       []EventConfig         `json:"span_events,omitempty"`
	SpanLinks        []LinkConfig          `json:"span_links,omitempty"`
}

type Config struct {
	Name     string                   `json:"name"`
	Seed     int64                    `json:"seed"`
	Services map[string]ServiceConfig `json:"services"`
	Nodes    map[string]NodeConfig    `json:"nodes"`
	Root     string                   `json:"root"`
	Edges    []EdgeConfig             `json:"edges"`
}

func LoadFromJSON(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer func() { _ = file.Close() }()
	return DecodeJSON(file)
}

func DecodeJSON(r io.Reader) (Config, error) {
	var cfg Config
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Config{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(c.Services) == 0 {
		return fmt.Errorf("services are required")
	}
	if len(c.Nodes) == 0 {
		return fmt.Errorf("nodes are required")
	}
	if strings.TrimSpace(c.Root) == "" {
		return fmt.Errorf("root is required")
	}
	if _, ok := c.Nodes[c.Root]; !ok {
		return fmt.Errorf("root node %q not found", c.Root)
	}
	if len(c.Edges) == 0 {
		return fmt.Errorf("edges are required")
	}

	for serviceID, service := range c.Services {
		if strings.TrimSpace(serviceID) == "" {
			return fmt.Errorf("service id cannot be empty")
		}
		for key, value := range service.Resource {
			if err := value.Validate(fmt.Sprintf("service %s resource %q", serviceID, key)); err != nil {
				return err
			}
		}
	}

	for nodeID, node := range c.Nodes {
		if strings.TrimSpace(nodeID) == "" {
			return fmt.Errorf("node id cannot be empty")
		}
		if strings.TrimSpace(node.Service) == "" {
			return fmt.Errorf("node %s: service is required", nodeID)
		}
		if _, ok := c.Services[node.Service]; !ok {
			return fmt.Errorf("node %s: unknown service %q", nodeID, node.Service)
		}
	}

	for i, edge := range c.Edges {
		if strings.TrimSpace(edge.From) == "" {
			return fmt.Errorf("edge %d: from is required", i)
		}
		if strings.TrimSpace(edge.To) == "" {
			return fmt.Errorf("edge %d: to is required", i)
		}
		if _, ok := c.Nodes[edge.From]; !ok {
			return fmt.Errorf("edge %d: unknown from node %q", i, edge.From)
		}
		if _, ok := c.Nodes[edge.To]; !ok {
			return fmt.Errorf("edge %d: unknown to node %q", i, edge.To)
		}
		if edge.Kind != EdgeKindClientServer && edge.Kind != EdgeKindProducerConsumer && edge.Kind != EdgeKindInternal && edge.Kind != EdgeKindClientDatabase {
			return fmt.Errorf("edge %d: unsupported kind %q", i, edge.Kind)
		}
		if edge.Repeat <= 0 {
			return fmt.Errorf("edge %d: repeat must be > 0", i)
		}
		if edge.DurationMs <= 0 {
			return fmt.Errorf("edge %d: duration_ms must be > 0", i)
		}
		if edge.NetworkLatencyMs < 0 {
			return fmt.Errorf("edge %d: network_latency_ms must be >= 0", i)
		}
		if edge.NetworkLatencyMs > 0 && edge.Kind == EdgeKindInternal {
			return fmt.Errorf("edge %d: network_latency_ms is not supported on internal edges", i)
		}
		// 2*NetworkLatencyMs < DurationMs is checked in validateTimings.

		for key, value := range edge.SpanAttributes {
			if err := value.Validate(fmt.Sprintf("edge %d span attribute %q", i, key)); err != nil {
				return err
			}
		}
		for j, event := range edge.SpanEvents {
			if strings.TrimSpace(event.Name) == "" {
				return fmt.Errorf("edge %d event %d: name is required", i, j)
			}
			for key, value := range event.Attributes {
				if err := value.Validate(fmt.Sprintf("edge %d event %d attribute %q", i, j, key)); err != nil {
					return err
				}
			}
		}
		for j, link := range edge.SpanLinks {
			if strings.TrimSpace(link.Node) == "" {
				return fmt.Errorf("edge %d link %d: node is required", i, j)
			}
			if _, ok := c.Nodes[link.Node]; !ok {
				return fmt.Errorf("edge %d link %d: unknown node %q", i, j, link.Node)
			}
			for key, value := range link.Attributes {
				if err := value.Validate(fmt.Sprintf("edge %d link %d attribute %q", i, j, key)); err != nil {
					return err
				}
			}
		}
	}

	// Outgoing-edges index built once, reused by both validators.
	outgoing := make(map[string][]EdgeConfig, len(c.Nodes))
	for _, edge := range c.Edges {
		outgoing[edge.From] = append(outgoing[edge.From], edge)
	}
	if err := validateDAG(c.Nodes, c.Edges, c.Root, outgoing); err != nil {
		return err
	}
	// Timing checks require an acyclic, rooted DAG.
	if err := validateTimings(c.Edges, computeConfigSubtreeDurations(c.Root, outgoing)); err != nil {
		return err
	}
	return nil
}

// validateDAG enforces: (1) acyclic, (2) root has no incoming edges,
// (3) every node is reachable from root.
func validateDAG(nodes map[string]NodeConfig, edges []EdgeConfig, root string, outgoing map[string][]EdgeConfig) error {
	indegree := make(map[string]int, len(nodes))
	for id := range nodes {
		indegree[id] = 0
	}
	for _, edge := range edges {
		indegree[edge.To]++
	}

	if indegree[root] > 0 {
		return fmt.Errorf("root node %q must have no incoming edges, found %d", root, indegree[root])
	}

	// Kahn's cycle detection: prune in topological order; remainder = cycle.
	working := make(map[string]int, len(indegree))
	for id, d := range indegree {
		working[id] = d
	}
	queue := make([]string, 0, len(nodes))
	for id, degree := range working {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		visited++
		for _, edge := range outgoing[nodeID] {
			working[edge.To]--
			if working[edge.To] == 0 {
				queue = append(queue, edge.To)
			}
		}
	}
	if visited != len(nodes) {
		return fmt.Errorf("scenario must be a DAG (cycle detected)")
	}

	// Reachability from root via BFS; anything not visited is dead config.
	reached := make(map[string]bool, len(nodes))
	queue = append(queue[:0], root)
	reached[root] = true
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		for _, edge := range outgoing[nodeID] {
			if !reached[edge.To] {
				reached[edge.To] = true
				queue = append(queue, edge.To)
			}
		}
	}
	if len(reached) < len(nodes) {
		orphans := make([]string, 0, len(nodes)-len(reached))
		for id := range nodes {
			if !reached[id] {
				orphans = append(orphans, id)
			}
		}
		sort.Strings(orphans)
		return fmt.Errorf("nodes not reachable from root %q: %v", root, orphans)
	}

	return nil
}

// computeConfigSubtreeDurations returns, for every node reachable from
// rootID, the total scenario-time (ms) consumed by its outgoing subtree.
// Mirrors generator.computeSubtreeDurations but runs on EdgeConfig so it
// can be called from Config.Validate. Assumes the graph is acyclic.
func computeConfigSubtreeDurations(rootID string, outgoing map[string][]EdgeConfig) map[string]int64 {
	out := make(map[string]int64, len(outgoing)+1)
	var walk func(id string) int64
	walk = func(id string) int64 {
		if v, ok := out[id]; ok {
			return v
		}
		edges := outgoing[id]
		if len(edges) == 0 {
			out[id] = 0
			return 0
		}
		var total int64
		for _, edge := range edges {
			d := edge.DurationMs
			if d <= 0 {
				d = 1
			}
			step := d + walk(edge.To) + 1 // matches the runtime walker's +1ms gap
			total += int64(edge.Repeat) * step
		}
		out[id] = total
		return total
	}
	walk(rootID)
	return out
}

// validateTimings checks 2*NetworkLatencyMs < DurationMs for every pair
// edge with positive latency. The constraint itself is independent of
// subtree size, but the error message includes the computed server
// interval and subtree so the user sees the consequence, not just which
// rule failed.
func validateTimings(edges []EdgeConfig, subtreeDuration map[string]int64) error {
	for i, edge := range edges {
		if edge.NetworkLatencyMs <= 0 {
			continue
		}
		if edge.Kind == EdgeKindInternal {
			// Already rejected in per-edge syntactic checks; defensive.
			continue
		}
		if 2*edge.NetworkLatencyMs < edge.DurationMs {
			continue
		}
		subtree := subtreeDuration[edge.To]
		effDur := edge.DurationMs + subtree
		serverInterval := effDur - 2*edge.NetworkLatencyMs
		return fmt.Errorf(
			"edge %d (%s -> %s, kind=%s): network_latency_ms=%d incompatible with duration_ms=%d "+
				"(effective span duration = duration + subtree(%q) = %d+%d = %dms; "+
				"server interval would be effDur - 2*latency = %dms, leaving 0 or negative own-work tail; "+
				"need duration_ms > 2*network_latency_ms)",
			i, edge.From, edge.To, edge.Kind,
			edge.NetworkLatencyMs, edge.DurationMs,
			edge.To, edge.DurationMs, subtree, effDur,
			serverInterval,
		)
	}
	return nil
}
