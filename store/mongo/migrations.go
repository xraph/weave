package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/xraph/grove/drivers/mongodriver/mongomigrate"
	"github.com/xraph/grove/migrate"
)

// Migrations is the grove migration group for the Weave mongo store.
var Migrations = migrate.NewGroup("weave")

func init() {
	Migrations.MustRegister(
		&migrate.Migration{
			Name:    "create_weave_collections_indexes",
			Version: "20240101000000",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				coll := mexec.DB().Collection(colCollections)

				indexes := []mongo.IndexModel{
					{
						Keys: bson.D{
							{Key: "tenant_id", Value: 1},
							{Key: "name", Value: 1},
						},
						Options: options.Index().SetUnique(true),
					},
					{
						Keys:    bson.D{{Key: "tenant_id", Value: 1}},
						Options: options.Index().SetName("idx_weave_collections_tenant"),
					},
					{
						Keys:    bson.D{{Key: "created_at", Value: 1}},
						Options: options.Index().SetName("idx_weave_collections_created_at"),
					},
				}

				_, err := coll.Indexes().CreateMany(ctx, indexes)
				if err != nil {
					return fmt.Errorf("create weave_collections indexes: %w", err)
				}
				return nil
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				return mexec.DB().Collection(colCollections).Drop(ctx)
			},
		},
		&migrate.Migration{
			Name:    "create_weave_documents_indexes",
			Version: "20240101000001",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				coll := mexec.DB().Collection(colDocuments)

				indexes := []mongo.IndexModel{
					{
						Keys: bson.D{
							{Key: "collection_id", Value: 1},
							{Key: "content_hash", Value: 1},
						},
						Options: options.Index().SetUnique(true),
					},
					{
						Keys: bson.D{
							{Key: "collection_id", Value: 1},
							{Key: "state", Value: 1},
						},
						Options: options.Index().SetName("idx_weave_documents_collection"),
					},
					{
						Keys:    bson.D{{Key: "tenant_id", Value: 1}},
						Options: options.Index().SetName("idx_weave_documents_tenant"),
					},
					{
						Keys:    bson.D{{Key: "created_at", Value: 1}},
						Options: options.Index().SetName("idx_weave_documents_created_at"),
					},
				}

				_, err := coll.Indexes().CreateMany(ctx, indexes)
				if err != nil {
					return fmt.Errorf("create weave_documents indexes: %w", err)
				}
				return nil
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				return mexec.DB().Collection(colDocuments).Drop(ctx)
			},
		},
		&migrate.Migration{
			Name:    "create_weave_chunks_indexes",
			Version: "20240101000002",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				coll := mexec.DB().Collection(colChunks)

				indexes := []mongo.IndexModel{
					{
						Keys: bson.D{
							{Key: "document_id", Value: 1},
							{Key: "index", Value: 1},
						},
						Options: options.Index().SetName("idx_weave_chunks_document"),
					},
					{
						Keys:    bson.D{{Key: "collection_id", Value: 1}},
						Options: options.Index().SetName("idx_weave_chunks_collection"),
					},
					{
						Keys:    bson.D{{Key: "tenant_id", Value: 1}},
						Options: options.Index().SetName("idx_weave_chunks_tenant"),
					},
				}

				_, err := coll.Indexes().CreateMany(ctx, indexes)
				if err != nil {
					return fmt.Errorf("create weave_chunks indexes: %w", err)
				}
				return nil
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				return mexec.DB().Collection(colChunks).Drop(ctx)
			},
		},
	)
}
