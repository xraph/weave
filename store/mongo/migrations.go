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

				if err := mexec.CreateCollection(ctx, (*collectionModel)(nil)); err != nil {
					return err
				}

				return mexec.CreateIndexes(ctx, colCollections, []mongo.IndexModel{
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
				})
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				return mexec.DropCollection(ctx, (*collectionModel)(nil))
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

				if err := mexec.CreateCollection(ctx, (*documentModel)(nil)); err != nil {
					return err
				}

				return mexec.CreateIndexes(ctx, colDocuments, []mongo.IndexModel{
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
				})
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				return mexec.DropCollection(ctx, (*documentModel)(nil))
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

				if err := mexec.CreateCollection(ctx, (*chunkModel)(nil)); err != nil {
					return err
				}

				return mexec.CreateIndexes(ctx, colChunks, []mongo.IndexModel{
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
				})
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				return mexec.DropCollection(ctx, (*chunkModel)(nil))
			},
		},
	)
}
