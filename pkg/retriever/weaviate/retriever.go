// Package weaviate provides a vector retriever backed by Weaviate's nearText GraphQL API.
// The server embeds query text internally; callers supply plain-text queries only.
package weaviate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	client "github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/graphql"
	"github.com/weaviate/weaviate/entities/models"
)

var _ interfaces.Retriever = (*WeaviateRetriever)(nil)

// WeaviateRetriever searches a Weaviate class via nearText and maps hits to interfaces.Document.
type WeaviateRetriever struct {
	// name is the stable identifier returned by [Name]; required, set via the first argument of [NewRetriever].
	name string

	// runtime fields — used by Search.
	className    string
	contentField string
	sourceField  string
	topK         int
	minScore     float64
	logger       logger.Logger
	client       *client.Client

	// build-time fields — consumed by NewRetriever; not used after construction.
	host     string
	scheme   string
	logLevel string
}

// Option configures WeaviateRetriever.
type Option func(*WeaviateRetriever)

// WithClient sets the Weaviate client.
func WithClient(client *client.Client) Option {
	return func(c *WeaviateRetriever) { c.client = client }
}

// WithHost sets the Weaviate host.
func WithHost(host string) Option {
	return func(c *WeaviateRetriever) { c.host = host }
}

// WithScheme sets the Weaviate scheme.
func WithScheme(scheme string) Option {
	return func(c *WeaviateRetriever) { c.scheme = scheme }
}

// WithContentField sets the Weaviate content field.
func WithContentField(contentField string) Option {
	return func(c *WeaviateRetriever) { c.contentField = contentField }
}

// WithSourceField sets the Weaviate source field.
func WithSourceField(sourceField string) Option {
	return func(c *WeaviateRetriever) { c.sourceField = sourceField }
}

// WithClassName sets the Weaviate class name.
func WithClassName(className string) Option {
	return func(c *WeaviateRetriever) { c.className = className }
}

// WithTopK sets the maximum number of documents returned per search.
func WithTopK(topK int) Option {
	return func(c *WeaviateRetriever) { c.topK = topK }
}

// WithMinScore sets the Weaviate minimum score.
func WithMinScore(minScore float64) Option {
	return func(c *WeaviateRetriever) { c.minScore = minScore }
}

// WithLogger sets the Weaviate logger.
func WithLogger(logger logger.Logger) Option {
	return func(c *WeaviateRetriever) { c.logger = logger }
}

// WithLogLevel sets the Weaviate log level.
func WithLogLevel(logLevel string) Option {
	return func(c *WeaviateRetriever) { c.logLevel = logLevel }
}

// NewRetriever builds a WeaviateRetriever. name is the stable identifier returned by [Name] and
// must be non-empty and unique across all retrievers registered with the same agent.
// className is required. When WithClient is omitted, host is required and scheme defaults to
// [types.DefaultScheme]. Zero-valued topK and minScore default to [types.DefaultTopK] and
// [types.DefaultMinScore].
func NewRetriever(name string, opts ...Option) (*WeaviateRetriever, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("name is required and must be non-empty")
	}
	r := &WeaviateRetriever{name: strings.TrimSpace(name)}
	for _, opt := range opts {
		opt(r)
	}
	if r.className == "" {
		return nil, errors.New("className is required")
	}
	if r.contentField == "" {
		r.contentField = types.DefaultContentField
	}
	if r.sourceField == "" {
		r.sourceField = types.DefaultSourceField
	}
	if r.topK == 0 {
		r.topK = types.DefaultTopK
	}
	if r.minScore == 0 {
		r.minScore = types.DefaultMinScore
	}
	if r.logLevel == "" {
		r.logLevel = "error"
	}
	if r.logger == nil {
		r.logger = logger.DefaultLogger(r.logLevel)
	}
	if r.client == nil {
		if r.host == "" {
			return nil, errors.New("host is required when not using WithClient")
		}
		if r.scheme == "" {
			r.scheme = types.DefaultScheme
		}
		weaviateConfig := client.Config{
			Scheme: r.scheme,
			Host:   r.host,
		}
		client, err := client.NewClient(weaviateConfig)
		if err != nil {
			return nil, fmt.Errorf("create weaviate client: %w", err)
		}
		r.client = client
	}
	r.logger.Info(context.Background(), "weaviate retriever built",
		slog.String("scope", "weaviate"),
		slog.String("name", r.name),
		slog.String("class", r.className),
		slog.Int("topK", r.topK),
		slog.Float64("minScore", r.minScore),
		slog.String("contentField", r.contentField),
		slog.String("sourceField", r.sourceField),
	)
	return r, nil
}

// Name implements [interfaces.Retriever].
func (r *WeaviateRetriever) Name() string {
	return r.name
}

// Search runs a nearText GraphQL query against the configured class and returns ranked documents.
func (r *WeaviateRetriever) Search(ctx context.Context, query string) ([]interfaces.Document, error) {
	if r.client == nil {
		return nil, errors.New("client is not set")
	}

	r.logger.Debug(ctx, "weaviate search start",
		slog.String("scope", "weaviate"),
		slog.String("name", r.name),
		slog.String("class", r.className),
		slog.String("query", query),
		slog.Int("topK", r.topK),
		slog.Float64("minScore", r.minScore),
	)
	start := time.Now()

	fields := []graphql.Field{
		{Name: r.contentField},
		{Name: r.sourceField},
		{Name: "_additional { certainty }"},
	}
	nearText := r.client.GraphQL().
		NearTextArgBuilder().
		WithConcepts([]string{query}).
		WithCertainty(float32(r.minScore))

	result, err := r.client.GraphQL().Get().
		WithClassName(r.className).
		WithNearText(nearText).
		WithLimit(r.topK).
		WithFields(fields...).
		Do(ctx)
	if err != nil {
		r.logger.Error(ctx, "weaviate search failed",
			slog.String("scope", "weaviate"),
			slog.String("name", r.name),
			slog.String("class", r.className),
			slog.Duration("elapsed", time.Since(start)),
			slog.Any("error", err),
		)
		return nil, err
	}

	docs, err := r.parseDocuments(ctx, result)
	if err != nil {
		r.logger.Error(ctx, "weaviate parse response failed",
			slog.String("scope", "weaviate"),
			slog.String("name", r.name),
			slog.String("class", r.className),
			slog.Duration("elapsed", time.Since(start)),
			slog.Any("error", err),
		)
		return nil, err
	}

	r.logger.Debug(ctx, "weaviate search done",
		slog.String("scope", "weaviate"),
		slog.String("name", r.name),
		slog.String("class", r.className),
		slog.Int("docs", len(docs)),
		slog.Duration("elapsed", time.Since(start)),
	)
	return docs, nil
}

// parseDocuments maps a Weaviate GraphQL response into []interfaces.Document.
// Shape: data.Get[className][]object with content/source and _additional.certainty.
func (r *WeaviateRetriever) parseDocuments(ctx context.Context, result *models.GraphQLResponse) ([]interfaces.Document, error) {
	if result == nil || result.Data == nil {
		return nil, nil
	}

	get, ok := result.Data["Get"].(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid response: missing Get")
	}

	items, ok := get[r.className].([]interface{})
	if !ok {
		return nil, errors.New("invalid response: missing class data")
	}

	var docs []interfaces.Document
	for _, item := range items {
		obj, ok := item.(map[string]interface{})
		if !ok {
			r.logger.Warn(ctx, "weaviate: skipping non-object item in response",
				slog.String("scope", "weaviate"),
				slog.String("name", r.name),
				slog.String("class", r.className),
			)
			continue
		}

		doc := interfaces.Document{
			Content:  getString(obj, r.contentField),
			Source:   getString(obj, r.sourceField),
			Metadata: obj,
		}
		if additional, ok := obj["_additional"].(map[string]interface{}); ok {
			if certainty, ok := additional["certainty"].(float64); ok {
				doc.Score = certainty
			}
		}

		docs = append(docs, doc)
	}
	return docs, nil
}

// getString reads a string property from a Weaviate object map, or "" if missing or wrong type.
func getString(obj map[string]interface{}, key string) string {
	if v, ok := obj[key].(string); ok {
		return v
	}
	return ""
}
