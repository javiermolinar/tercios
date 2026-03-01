package scenario

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type Generator struct {
	definition Definition
	outgoing   map[string][]Edge
	counter    atomic.Uint64
}

func NewGenerator(definition Definition) *Generator {
	outgoing := make(map[string][]Edge, len(definition.Nodes))
	for _, edge := range definition.Edges {
		outgoing[edge.From] = append(outgoing[edge.From], edge)
	}
	return &Generator{definition: definition, outgoing: outgoing}
}

func (g *Generator) GenerateBatch(_ context.Context) ([]model.Span, error) {
	if g == nil {
		return nil, fmt.Errorf("scenario generator not configured")
	}
	if len(g.definition.Nodes) == 0 {
		return nil, fmt.Errorf("scenario definition has no nodes")
	}

	sequence := g.counter.Add(1)
	traceID := traceIDFromSeed(g.definition.Seed, sequence)
	idState := newSpanIDState(g.definition.Seed, sequence)

	estimated := estimateDuration(g.definition.Root, g.outgoing)
	if estimated <= 0 {
		estimated = 100 * time.Millisecond
	}
	base := time.Now().UTC()

	rootNode, ok := g.definition.Nodes[g.definition.Root]
	if !ok {
		return nil, fmt.Errorf("root node %q not found", g.definition.Root)
	}

	rootSpan := g.newSpan(traceID, idState.next(), oteltrace.SpanID{}, rootNode, oteltrace.SpanKindInternal, base, estimated, nil)
	spans := []model.Span{rootSpan}

	cursor := base.Add(1 * time.Millisecond)
	spans = g.emitFromNode(spans, traceID, rootSpan.SpanID, g.definition.Root, &cursor, idState)
	return spans, nil
}

type spanIDState struct {
	seed   uint64
	seq    uint64
	nextID atomic.Uint64
}

func newSpanIDState(seed int64, sequence uint64) *spanIDState {
	return &spanIDState{seed: uint64(seed), seq: sequence}
}

func (s *spanIDState) next() oteltrace.SpanID {
	index := s.nextID.Add(1)
	v := splitmix64(s.seed ^ s.seq ^ index)
	if v == 0 {
		v = 1
	}
	var id oteltrace.SpanID
	binary.BigEndian.PutUint64(id[:], v)
	return id
}

func (g *Generator) emitFromNode(
	spans []model.Span,
	traceID oteltrace.TraceID,
	parentSpanID oteltrace.SpanID,
	nodeID string,
	cursor *time.Time,
	idState *spanIDState,
) []model.Span {
	edges := g.outgoing[nodeID]
	if len(edges) == 0 {
		return spans
	}

	for _, edge := range edges {
		for i := 0; i < edge.Repeat; i++ {
			sourceNode := g.definition.Nodes[edge.From]
			targetNode := g.definition.Nodes[edge.To]
			start := *cursor
			duration := edge.Duration
			if duration <= 0 {
				duration = 1 * time.Millisecond
			}

			switch edge.Kind {
			case EdgeKindClientServer:
				clientSpan := g.newSpan(traceID, idState.next(), parentSpanID, sourceNode, oteltrace.SpanKindClient, start, duration, edge.SpanAttributes)
				clientSpan.Name = edgeSpanName(sourceNode, targetNode)
				spans = append(spans, clientSpan)

				serverSpan := g.newSpan(traceID, idState.next(), clientSpan.SpanID, targetNode, oteltrace.SpanKindServer, start, duration, edge.SpanAttributes)
				spans = append(spans, serverSpan)

				*cursor = serverSpan.EndTime.Add(1 * time.Millisecond)
				spans = g.emitFromNode(spans, traceID, serverSpan.SpanID, edge.To, cursor, idState)

			case EdgeKindProducerConsumer:
				producerSpan := g.newSpan(traceID, idState.next(), parentSpanID, sourceNode, oteltrace.SpanKindProducer, start, duration, edge.SpanAttributes)
				producerSpan.Name = edgeSpanName(sourceNode, targetNode)
				spans = append(spans, producerSpan)

				consumerSpan := g.newSpan(traceID, idState.next(), producerSpan.SpanID, targetNode, oteltrace.SpanKindConsumer, start, duration, edge.SpanAttributes)
				spans = append(spans, consumerSpan)

				*cursor = consumerSpan.EndTime.Add(1 * time.Millisecond)
				spans = g.emitFromNode(spans, traceID, consumerSpan.SpanID, edge.To, cursor, idState)

			case EdgeKindClientDatabase:
				clientSpan := g.newSpan(traceID, idState.next(), parentSpanID, sourceNode, oteltrace.SpanKindClient, start, duration, edge.SpanAttributes)
				clientSpan.Name = edgeSpanName(sourceNode, targetNode)
				spans = append(spans, clientSpan)

				dbSpan := g.newSpan(traceID, idState.next(), clientSpan.SpanID, targetNode, oteltrace.SpanKindServer, start, duration, edge.SpanAttributes)
				spans = append(spans, dbSpan)

				*cursor = dbSpan.EndTime.Add(1 * time.Millisecond)
				spans = g.emitFromNode(spans, traceID, dbSpan.SpanID, edge.To, cursor, idState)

			case EdgeKindInternal:
				internalSpan := g.newSpan(traceID, idState.next(), parentSpanID, targetNode, oteltrace.SpanKindInternal, start, duration, edge.SpanAttributes)
				spans = append(spans, internalSpan)

				*cursor = internalSpan.EndTime.Add(1 * time.Millisecond)
				spans = g.emitFromNode(spans, traceID, internalSpan.SpanID, edge.To, cursor, idState)
			}
		}
	}

	return spans
}

func (g *Generator) newSpan(
	traceID oteltrace.TraceID,
	spanID oteltrace.SpanID,
	parentSpanID oteltrace.SpanID,
	node Node,
	kind oteltrace.SpanKind,
	start time.Time,
	duration time.Duration,
	edgeAttrs map[string]attribute.Value,
) model.Span {
	service := g.definition.Services[node.Service]
	resourceAttrs := cloneAttributeValues(service.ResourceAttributes)
	attrs := map[string]attribute.Value{}
	if serviceName, ok := resourceAttrs["service.name"]; ok {
		attrs["service.name"] = serviceName
	}
	for key, value := range edgeAttrs {
		attrs[key] = value
	}

	name := node.SpanName
	if name == "" {
		name = node.ID
	}
	if duration <= 0 {
		duration = 1 * time.Millisecond
	}

	return model.Span{
		TraceID:            traceID,
		SpanID:             spanID,
		ParentSpanID:       parentSpanID,
		Name:               name,
		Kind:               kind,
		StartTime:          start,
		EndTime:            start.Add(duration),
		Attributes:         attrs,
		ResourceAttributes: resourceAttrs,
		StatusCode:         codes.Ok,
	}
}

func edgeSpanName(from Node, to Node) string {
	fromName := from.SpanName
	if fromName == "" {
		fromName = from.ID
	}
	toName := to.SpanName
	if toName == "" {
		toName = to.ID
	}
	return fmt.Sprintf("%s -> %s", fromName, toName)
}

func cloneAttributeValues(values map[string]attribute.Value) map[string]attribute.Value {
	if len(values) == 0 {
		return map[string]attribute.Value{}
	}
	copy := make(map[string]attribute.Value, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func estimateDuration(nodeID string, outgoing map[string][]Edge) time.Duration {
	memo := map[string]time.Duration{}
	var walk func(id string) time.Duration
	walk = func(id string) time.Duration {
		if value, ok := memo[id]; ok {
			return value
		}
		edges := outgoing[id]
		if len(edges) == 0 {
			memo[id] = 0
			return 0
		}
		total := time.Duration(0)
		for _, edge := range edges {
			duration := edge.Duration
			if duration <= 0 {
				duration = 1 * time.Millisecond
			}
			subtree := walk(edge.To)
			step := duration + subtree + 1*time.Millisecond
			total += time.Duration(edge.Repeat) * step
		}
		memo[id] = total
		return total
	}
	value := walk(nodeID)
	if value <= 0 {
		return 1 * time.Millisecond
	}
	return value
}

func traceIDFromSeed(seed int64, sequence uint64) oteltrace.TraceID {
	a := splitmix64(uint64(seed) ^ sequence)
	b := splitmix64(a ^ 0x9e3779b97f4a7c15)
	var id oteltrace.TraceID
	binary.BigEndian.PutUint64(id[0:8], a)
	binary.BigEndian.PutUint64(id[8:16], b)
	if id.IsValid() {
		return id
	}
	id[15] = 1
	return id
}

func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}
