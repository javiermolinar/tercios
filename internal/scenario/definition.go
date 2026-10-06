package scenario

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type EventDef struct {
	Name       string
	Attributes []attribute.KeyValue
}

type LinkDef struct {
	Node       string
	Attributes []attribute.KeyValue
}

type Service struct {
	ID                 string
	ResourceAttributes map[string]attribute.Value
}

type Node struct {
	ID       string
	Service  string
	SpanName string

	// Resolved fields for direct construction. Legacy call expansion derives
	// these from edges instead. Generation support for direct nodes is pending.
	Parent            string
	Kind              oteltrace.SpanKind
	StatusCode        codes.Code
	StatusDescription string
	StartOffset       time.Duration
	Duration          time.Duration
	SpanAttributes    map[string]attribute.Value
	SpanEvents        []EventDef
	SpanLinks         []LinkDef
}

type Edge struct {
	From           string
	To             string
	Kind           EdgeKind
	Repeat         int
	Duration       time.Duration
	NetworkLatency time.Duration
	SpanAttributes map[string]attribute.Value
	SpanEvents     []EventDef
	SpanLinks      []LinkDef
}

type Definition struct {
	Name     string
	Seed     int64
	Root     string
	Services map[string]Service
	Nodes    map[string]Node
	Edges    []Edge

	// Retain the construction mode so unsupported direct scenarios cannot
	// accidentally enter call expansion, even when an optional root is set.
	direct bool
}

func (d Definition) checkGenerationSupport() error {
	if d.direct {
		return fmt.Errorf("direct node generation is not implemented")
	}
	return nil
}

func (c Config) Build() (Definition, error) {
	var nodes map[string]Node
	if c.hasDirectNodes() {
		var err error
		nodes, err = c.compileDirectNodes()
		if err != nil {
			return Definition{}, err
		}
	} else if err := c.Validate(); err != nil {
		return Definition{}, err
	}

	definition := Definition{
		Name:     c.Name,
		Seed:     c.Seed,
		Root:     c.Root,
		Services: make(map[string]Service, len(c.Services)),
		Nodes:    nodes,
		direct:   nodes != nil,
	}

	for id, service := range c.Services {
		attrs, err := typedMapToAttributes(service.Resource, fmt.Sprintf("service %s resource", id))
		if err != nil {
			return Definition{}, err
		}
		definition.Services[id] = Service{ID: id, ResourceAttributes: attrs}
	}

	if nodes != nil {
		return definition, nil
	}

	definition.Nodes = make(map[string]Node, len(c.Nodes))
	for id, node := range c.Nodes {
		definition.Nodes[id] = Node{ID: id, Service: node.Service, SpanName: node.SpanName}
	}
	definition.Edges = make([]Edge, 0, len(c.Edges))

	for i, edge := range c.Edges {
		spanAttrs, err := typedMapToAttributes(edge.SpanAttributes, fmt.Sprintf("edge %d span", i))
		if err != nil {
			return Definition{}, err
		}
		events, err := buildEventDefs(edge.SpanEvents, fmt.Sprintf("edge %d event", i))
		if err != nil {
			return Definition{}, err
		}
		links, err := buildLinkDefs(edge.SpanLinks, fmt.Sprintf("edge %d link", i))
		if err != nil {
			return Definition{}, err
		}
		definition.Edges = append(definition.Edges, Edge{
			From:           edge.From,
			To:             edge.To,
			Kind:           edge.Kind,
			Repeat:         edge.Repeat,
			Duration:       time.Duration(edge.DurationMs) * time.Millisecond,
			NetworkLatency: time.Duration(edge.NetworkLatencyMs) * time.Millisecond,
			SpanAttributes: spanAttrs,
			SpanEvents:     events,
			SpanLinks:      links,
		})
	}

	return definition, nil
}

// Validate before conversion: ToAttributeValue does not enforce every typed
// value rule, including the positive size requirement for generated strings.
func typedMapToAttributes(values map[string]TypedValue, context string) (map[string]attribute.Value, error) {
	result := make(map[string]attribute.Value, len(values))
	for _, key := range sortedKeys(values) {
		value := values[key]
		label := fmt.Sprintf("%s attribute %q", context, key)
		if err := value.Validate(label); err != nil {
			return nil, err
		}
		attrValue, err := value.ToAttributeValue()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		result[key] = attrValue
	}
	return result, nil
}

func buildEventDefs(configs []EventConfig, context string) ([]EventDef, error) {
	if len(configs) == 0 {
		return nil, nil
	}
	out := make([]EventDef, 0, len(configs))
	for i, cfg := range configs {
		label := fmt.Sprintf("%s %d", context, i)
		if strings.TrimSpace(cfg.Name) == "" {
			return nil, fmt.Errorf("%s: name is required", label)
		}
		attrs, err := typedMapToAttributes(cfg.Attributes, label)
		if err != nil {
			return nil, err
		}
		out = append(out, EventDef{Name: cfg.Name, Attributes: model.AttributesFromMap(attrs)})
	}
	return out, nil
}

func buildLinkDefs(configs []LinkConfig, context string) ([]LinkDef, error) {
	if len(configs) == 0 {
		return nil, nil
	}
	out := make([]LinkDef, 0, len(configs))
	for i, cfg := range configs {
		label := fmt.Sprintf("%s %d", context, i)
		if strings.TrimSpace(cfg.Node) == "" {
			return nil, fmt.Errorf("%s: node is required", label)
		}
		attrs, err := typedMapToAttributes(cfg.Attributes, label)
		if err != nil {
			return nil, err
		}
		out = append(out, LinkDef{Node: cfg.Node, Attributes: model.AttributesFromMap(attrs)})
	}
	return out, nil
}

// compileDirectNodes resolves native span fields on existing nodes. Connections
// supply candidate parents; each node is configured once, so no merging is needed.
func (c Config) compileDirectNodes() (map[string]Node, error) {
	if err := c.validateHeader(); err != nil {
		return nil, err
	}
	if len(c.Edges) == 0 {
		return nil, fmt.Errorf("edges are required")
	}
	if err := c.validateNodes(); err != nil {
		return nil, err
	}
	parents := make(map[string]map[string]struct{}, len(c.Nodes))
	endpoint := func(id, context string) error {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("%s is required", context)
		}
		if _, ok := c.Nodes[id]; !ok {
			return fmt.Errorf("%s: unknown node %q", context, id)
		}
		if parents[id] == nil {
			parents[id] = map[string]struct{}{}
		}
		return nil
	}
	for i, edge := range c.Edges {
		if err := validateDirectEdge(edge, i); err != nil {
			return nil, err
		}
		if err := endpoint(edge.From, fmt.Sprintf("edge %d from", i)); err != nil {
			return nil, err
		}
		if edge.To != "" {
			if err := endpoint(edge.To, fmt.Sprintf("edge %d to", i)); err != nil {
				return nil, err
			}
			parents[edge.To][edge.From] = struct{}{}
		}
	}
	nodes := make(map[string]Node, len(c.Nodes))
	for _, id := range sortedKeys(c.Nodes) {
		if parents[id] == nil {
			return nil, fmt.Errorf("node %q: not referenced by any from/to endpoint", id)
		}
		node, err := c.Nodes[id].buildDirect(id, parents[id])
		if err != nil {
			return nil, err
		}
		nodes[id] = node
	}
	if err := validateNodeForest(nodes); err != nil {
		return nil, err
	}
	if c.rootPresent && strings.TrimSpace(c.Root) == "" {
		return nil, fmt.Errorf("root must be a nonempty node reference when supplied")
	}
	if c.Root != "" {
		root, ok := nodes[c.Root]
		if !ok {
			return nil, fmt.Errorf("root node %q not found", c.Root)
		}
		if root.Parent != "" {
			return nil, fmt.Errorf("root node %q has effective parent %q", c.Root, root.Parent)
		}
	}
	return nodes, nil
}

func (c NodeConfig) buildDirect(id string, parents map[string]struct{}) (Node, error) {
	node := Node{ID: id, Service: c.Service, SpanName: c.SpanName}
	fail := func(err error) (Node, error) {
		return Node{}, fmt.Errorf("node %q: %w", id, err)
	}
	if node.SpanName == "" {
		node.SpanName = id
	} else if strings.TrimSpace(node.SpanName) == "" {
		return fail(fmt.Errorf("span_name must be nonempty"))
	}
	var err error
	node.Kind, err = nativeSpanKind(c.Kind)
	if err != nil {
		return fail(err)
	}
	node.StatusCode, node.StatusDescription, err = nativeSpanStatus(c.Status)
	if err != nil {
		return fail(err)
	}
	offset, duration := int64(0), int64(1)
	if c.StartOffsetMs != nil {
		offset = *c.StartOffsetMs
	}
	if c.DurationMs != nil {
		duration = *c.DurationMs
	}
	const maxMs = math.MaxInt64 / int64(time.Millisecond)
	if offset < 0 || offset > maxMs {
		return fail(fmt.Errorf("start_offset_ms out of range [0,%d]", maxMs))
	}
	if duration < 0 || duration > maxMs {
		return fail(fmt.Errorf("duration_ms out of range [0,%d]", maxMs))
	}
	if offset > maxMs-duration {
		return fail(fmt.Errorf("start_offset_ms + duration_ms overflows duration"))
	}
	node.StartOffset = time.Duration(offset) * time.Millisecond
	node.Duration = time.Duration(duration) * time.Millisecond
	if len(c.Parent) != 0 {
		var parent *string
		if err := json.Unmarshal(c.Parent, &parent); err != nil {
			return fail(fmt.Errorf("parent must be null or a node reference: %w", err))
		}
		if parent != nil {
			if strings.TrimSpace(*parent) == "" {
				return fail(fmt.Errorf("parent node reference must be nonempty"))
			}
			node.Parent = *parent
		}
	} else {
		if len(parents) > 1 {
			return fail(fmt.Errorf("parent is ambiguous: candidates %v", sortedKeys(parents)))
		}
		for parent := range parents {
			node.Parent = parent
		}
	}
	node.SpanAttributes, err = typedMapToAttributes(c.SpanAttributes, "span_attributes")
	if err != nil {
		return fail(err)
	}
	node.SpanEvents, err = buildEventDefs(c.SpanEvents, id+" span_events")
	if err != nil {
		return fail(err)
	}
	node.SpanLinks, err = buildLinkDefs(c.SpanLinks, id+" span_links")
	if err != nil {
		return fail(err)
	}
	return node, nil
}

func nativeSpanKind(value *string) (oteltrace.SpanKind, error) {
	if value == nil {
		return oteltrace.SpanKindUnspecified, nil
	}
	kinds := map[string]oteltrace.SpanKind{
		"UNSPECIFIED": oteltrace.SpanKindUnspecified, "INTERNAL": oteltrace.SpanKindInternal,
		"SERVER": oteltrace.SpanKindServer, "CLIENT": oteltrace.SpanKindClient,
		"PRODUCER": oteltrace.SpanKindProducer, "CONSUMER": oteltrace.SpanKindConsumer,
	}
	kind, ok := kinds[*value]
	if !ok {
		return 0, fmt.Errorf("unsupported native kind %q", *value)
	}
	return kind, nil
}

func nativeSpanStatus(value *NodeStatusConfig) (codes.Code, string, error) {
	code, description := "UNSET", ""
	if value != nil {
		if value.Code != nil {
			code = *value.Code
		}
		if value.Description != nil {
			description = *value.Description
		}
	}
	status, ok := map[string]codes.Code{"UNSET": codes.Unset, "OK": codes.Ok, "ERROR": codes.Error}[code]
	if !ok {
		return 0, "", fmt.Errorf("unsupported status code %q", code)
	}
	if description != "" && status != codes.Error {
		return 0, "", fmt.Errorf("status description requires code ERROR")
	}
	return status, description, nil
}
