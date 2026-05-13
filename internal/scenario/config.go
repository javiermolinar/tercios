package scenario

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	// Repeat is how many times this edge fires sequentially under its
	// source node. Each repeat materializes a fresh span (or pair of
	// spans) and a fresh recursive subtree.
	Repeat int `json:"repeat"`
	// DurationMs is the edge's own "work" time, in milliseconds, not
	// counting its subtree. The full span(s) duration is
	// DurationMs + subtreeDuration[To] so the resulting span temporally
	// contains its descendants.
	DurationMs int64 `json:"duration_ms"`
	// NetworkLatencyMs is the symmetric one-way network gap between the
	// source and target sides of a pair edge (ClientServer,
	// ProducerConsumer, ClientDatabase). The target-side span is inset by
	// this amount on both sides of the source-side span's interval,
	// modeling request-travel and response-travel time. Zero (the
	// default) preserves the historical behavior where both spans share
	// exactly the same start and end. Must be zero for Internal edges,
	// where no client/server distinction exists. Validation enforces
	// 2 * NetworkLatencyMs < DurationMs so the target interval stays
	// strictly inside the source interval and leaves at least 1ms of
	// post-children own-work tail.
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
		if edge.NetworkLatencyMs > 0 {
			if edge.Kind == EdgeKindInternal {
				return fmt.Errorf("edge %d: network_latency_ms is not supported on internal edges", i)
			}
			if 2*edge.NetworkLatencyMs >= edge.DurationMs {
				return fmt.Errorf("edge %d: 2 * network_latency_ms (%d) must be < duration_ms (%d)", i, 2*edge.NetworkLatencyMs, edge.DurationMs)
			}
		}
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

	if err := validateDAG(c.Nodes, c.Edges); err != nil {
		return err
	}

	return nil
}

func validateDAG(nodes map[string]NodeConfig, edges []EdgeConfig) error {
	indegree := make(map[string]int, len(nodes))
	adjacency := make(map[string][]string, len(nodes))
	for id := range nodes {
		indegree[id] = 0
	}

	for _, edge := range edges {
		adjacency[edge.From] = append(adjacency[edge.From], edge.To)
		indegree[edge.To]++
	}

	queue := make([]string, 0, len(nodes))
	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}

	visited := 0
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		visited++

		for _, child := range adjacency[nodeID] {
			indegree[child]--
			if indegree[child] == 0 {
				queue = append(queue, child)
			}
		}
	}

	if visited != len(nodes) {
		return fmt.Errorf("scenario must be a DAG (cycle detected)")
	}
	return nil
}
