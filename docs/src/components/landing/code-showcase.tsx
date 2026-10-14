"use client";

import { motion } from "framer-motion";
import { CodeBlock } from "./code-block";
import { SectionHeader } from "./section-header";

const ingestCode = `package main

import (
  "context"
  "fmt"
  "log"
  "os"

  "github.com/xraph/grove"
  "github.com/xraph/grove/drivers/pgdriver"
  "github.com/xraph/weave"
  "github.com/xraph/weave/chunker"
  "github.com/xraph/weave/collection"
  "github.com/xraph/weave/embedder"
  "github.com/xraph/weave/engine"
  "github.com/xraph/weave/store/postgres"
  "github.com/xraph/weave/vectorstore/pgvector"
)

func main() {
  ctx := context.Background()

  pg := pgdriver.New()
  if err := pg.Open(ctx, os.Getenv("DATABASE_URL")); err != nil {
    log.Fatal(err)
  }
  db, err := grove.Open(pg)
  if err != nil {
    log.Fatal(err)
  }
  st := postgres.New(db)
  if err := st.Migrate(ctx); err != nil {
    log.Fatal(err)
  }

  eng, err := engine.New(
    engine.WithStore(st),
    engine.WithVectorStore(pgvector.New(db)),
    engine.WithEmbedder(embedder.NewOpenAIEmbedder(os.Getenv("OPENAI_API_KEY"))),
    engine.WithChunker(chunker.NewRecursiveChunker()),
  )
  if err != nil {
    log.Fatal(err)
  }

  ctx = weave.WithTenant(ctx, "tenant-1")
  ctx = weave.WithApp(ctx, "myapp")

  col := &collection.Collection{Name: "product-faq"}
  if err := eng.CreateCollection(ctx, col); err != nil {
    log.Fatal(err)
  }

  // Ingest a document: chunk, embed, store
  res, err := eng.Ingest(ctx, &engine.IngestInput{
    CollectionID: col.ID,
    Title:        "Product FAQ",
    Content:      "Our return policy...",
    Source:       "faq.md",
  })
  if err != nil {
    log.Fatal(err)
  }
  fmt.Println(res.State, res.ChunkCount) // ready 12
}`;

const retrieveCode = `package main

import (
  "context"
  "fmt"

  "github.com/xraph/weave"
  "github.com/xraph/weave/engine"
  "github.com/xraph/weave/id"
)

func queryContext(
  ctx context.Context,
  eng *engine.Engine,
  colID id.CollectionID,
) error {
  ctx = weave.WithTenant(ctx, "tenant-1")

  // Semantic retrieval with a score threshold
  hits, err := eng.Retrieve(ctx, "What is the return policy?",
    engine.WithCollection(colID),
    engine.WithTopK(5),
    engine.WithMinScore(0.75),
  )
  if err != nil {
    return err
  }

  for _, h := range hits {
    fmt.Printf("[%.2f] %s\\n", h.Score, h.Chunk.Content)
  }
  // [0.94] Our return policy allows...
  // [0.87] Items must be returned...
  return nil
}`;

export function CodeShowcase() {
  return (
    <section className="relative w-full py-20 sm:py-28">
      <div className="container max-w-(--fd-layout-width) mx-auto px-4 sm:px-6">
        <SectionHeader
          badge="Developer Experience"
          title="Simple API. Powerful retrieval."
          description="Open a database, build an engine, ingest a document, then retrieve the chunks closest to a question. Weave chunks, embeds and stores in between."
        />

        <div className="mt-14 grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Ingestion side */}
          <motion.div
            initial={{ opacity: 0, x: -20 }}
            whileInView={{ opacity: 1, x: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.5, delay: 0.1 }}
          >
            <div className="mb-3 flex items-center gap-2">
              <div className="size-2 rounded-full bg-violet-500" />
              <span className="text-xs font-medium text-fd-muted-foreground uppercase tracking-wider">
                Ingestion
              </span>
            </div>
            <CodeBlock code={ingestCode} filename="main.go" />
          </motion.div>

          {/* Retrieval side */}
          <motion.div
            initial={{ opacity: 0, x: 20 }}
            whileInView={{ opacity: 1, x: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.5, delay: 0.2 }}
          >
            <div className="mb-3 flex items-center gap-2">
              <div className="size-2 rounded-full bg-green-500" />
              <span className="text-xs font-medium text-fd-muted-foreground uppercase tracking-wider">
                Retrieval
              </span>
            </div>
            <CodeBlock code={retrieveCode} filename="retrieve.go" />
          </motion.div>
        </div>
      </div>
    </section>
  );
}
