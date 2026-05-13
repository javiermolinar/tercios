package scenario

import (
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
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
}

func (c Config) Build() (Definition, error) {
	if err := c.Validate(); err != nil {
		return Definition{}, err
	}

	definition := Definition{
		Name:     c.Name,
		Seed:     c.Seed,
		Root:     c.Root,
		Services: make(map[string]Service, len(c.Services)),
		Nodes:    make(map[string]Node, len(c.Nodes)),
		Edges:    make([]Edge, 0, len(c.Edges)),
	}

	for id, service := range c.Services {
		attrs, err := typedMapToAttributes(service.Resource)
		if err != nil {
			return Definition{}, fmt.Errorf("service %s: %w", id, err)
		}
		definition.Services[id] = Service{ID: id, ResourceAttributes: attrs}
	}

	for id, node := range c.Nodes {
		definition.Nodes[id] = Node{ID: id, Service: node.Service, SpanName: node.SpanName}
	}

	for i, edge := range c.Edges {
		spanAttrs, err := typedMapToAttributes(edge.SpanAttributes)
		if err != nil {
			return Definition{}, fmt.Errorf("edge %d: %w", i, err)
		}
		events, err := buildEventDefs(edge.SpanEvents, i)
		if err != nil {
			return Definition{}, err
		}
		links, err := buildLinkDefs(edge.SpanLinks, i)
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

func typedMapToAttributes(values map[string]TypedValue) (map[string]attribute.Value, error) {
	if len(values) == 0 {
		return map[string]attribute.Value{}, nil
	}

	result := make(map[string]attribute.Value, len(values))
	for key, value := range values {
		attrValue, err := typedValueToAttributeValue(value)
		if err != nil {
			return nil, fmt.Errorf("attribute %q: %w", key, err)
		}
		result[key] = attrValue
	}
	return result, nil
}

func typedValueToAttributeValue(value TypedValue) (attribute.Value, error) {
	return value.ToAttributeValue()
}

func buildEventDefs(configs []EventConfig, edgeIndex int) ([]EventDef, error) {
	if len(configs) == 0 {
		return nil, nil
	}
	out := make([]EventDef, 0, len(configs))
	for j, cfg := range configs {
		attrs, err := typedMapToKeyValues(cfg.Attributes)
		if err != nil {
			return nil, fmt.Errorf("edge %d event %d: %w", edgeIndex, j, err)
		}
		out = append(out, EventDef{Name: cfg.Name, Attributes: attrs})
	}
	return out, nil
}

func buildLinkDefs(configs []LinkConfig, edgeIndex int) ([]LinkDef, error) {
	if len(configs) == 0 {
		return nil, nil
	}
	out := make([]LinkDef, 0, len(configs))
	for j, cfg := range configs {
		attrs, err := typedMapToKeyValues(cfg.Attributes)
		if err != nil {
			return nil, fmt.Errorf("edge %d link %d: %w", edgeIndex, j, err)
		}
		out = append(out, LinkDef{Node: cfg.Node, Attributes: attrs})
	}
	return out, nil
}

func typedMapToKeyValues(values map[string]TypedValue) ([]attribute.KeyValue, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]attribute.KeyValue, 0, len(values))
	for key, value := range values {
		attrValue, err := typedValueToAttributeValue(value)
		if err != nil {
			return nil, fmt.Errorf("attribute %q: %w", key, err)
		}
		out = append(out, attribute.KeyValue{Key: attribute.Key(key), Value: attrValue})
	}
	return out, nil
}
