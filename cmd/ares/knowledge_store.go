// knowledge_store — operator commands for the AKG's portable snapshot (M3):
// `ares knowledge export` writes one namespace to a JSON file and `ares
// knowledge import` reads it back.
//
// These are OPERATOR commands, deliberately not MCP tools: an export dumps a
// whole namespace, and handing a model a callable tool that does that would turn
// every prompt into a data-exfiltration surface.
//
// They open the configured store DIRECTLY (the way `ares db migrate` does)
// rather than going through a running server, because the AKG store lives inside
// the serve process and the CLI has no other way to reach it. The default store
// is in-memory, which a separate process can never read, so a non-Postgres
// configuration fails loudly instead of exporting an empty corpus.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/spf13/cobra"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/knowledge"
	postgresstore "github.com/Timwood0x10/ares/internal/knowledge/store/postgres"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

var (
	knowledgeStoreConfig    string
	knowledgeStoreNamespace string
	knowledgeStoreOut       string
	knowledgeStoreIn        string
)

var knowledgeExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export a namespace's AKG knowledge to a portable JSON snapshot",
	Long: `Writes every object in one namespace — plus its embedding vectors, when the
store can enumerate them — to a JSON snapshot.

Reads the storage section of ares.yaml. The store must be the persistent
Postgres backend: the default in-memory store belongs to the serve process and
holds nothing a separate command can read.

  ares knowledge export --namespace default --out snapshot.json
  ares knowledge export --namespace default --out - > snapshot.json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		store, closeStore, err := openAKGStore(knowledgeStoreConfig)
		if err != nil {
			return err
		}
		defer closeStore()

		written, err := exportSnapshotTo(cmd.Context(), cmd.OutOrStdout(), store, knowledgeStoreNamespace)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "exported %d object(s) from namespace %q\n",
			written.Objects, written.Namespace)
		if written.Representations == 0 {
			// Fail loud about a lossy snapshot: without vectors a restore recalls
			// lexically only, and nothing downstream would say so.
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
				"warning: no embedding vectors were exported (the store cannot enumerate them); "+
					"a restore will recall lexically until vectors are regenerated")
		}
		return nil
	},
}

var knowledgeImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Import an AKG snapshot written by `ares knowledge export`",
	Long: `Reads a snapshot and persists its objects (and vectors) through the store's
normal write path, so every write-side guard — namespace isolation, schema
stamping, quality gate — still applies. Re-importing the same snapshot is
idempotent.

  ares knowledge import --in snapshot.json
  cat snapshot.json | ares knowledge import --in -`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		store, closeStore, err := openAKGStore(knowledgeStoreConfig)
		if err != nil {
			return err
		}
		defer closeStore()

		reader, closeReader, err := snapshotReader(knowledgeStoreIn)
		if err != nil {
			return err
		}
		defer closeReader()

		written, err := importSnapshotFrom(cmd.Context(), reader, store)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "imported %d object(s) and %d vector(s) into namespace %q\n",
			written.Objects, written.Representations, written.Namespace)
		return nil
	},
}

func init() {
	knowledgeCmd.AddCommand(knowledgeExportCmd, knowledgeImportCmd)

	for _, c := range []*cobra.Command{knowledgeExportCmd, knowledgeImportCmd} {
		c.Flags().StringVar(&knowledgeStoreConfig, "config", "ares.yaml",
			"Path to ares.yaml (storage section)")
	}
	knowledgeExportCmd.Flags().StringVar(&knowledgeStoreNamespace, "namespace", "",
		"Namespace to export (empty = every namespace)")
	knowledgeExportCmd.Flags().StringVar(&knowledgeStoreOut, "out", "-",
		"Snapshot file to write (\"-\" = stdout)")
	knowledgeImportCmd.Flags().StringVar(&knowledgeStoreIn, "in", "-",
		"Snapshot file to read (\"-\" = stdin)")
}

// openAKGStore opens the configured AKG store for an operator command.
//
// It refuses a non-Postgres configuration instead of falling back to the
// in-memory store the way bootstrap does: an in-memory store belongs to the
// process that created it, so a CLI "exporting nothing" would read as an empty
// knowledge base rather than as a misconfiguration.
func openAKGStore(configPath string) (knowledge.KnowledgeStore, func(), error) {
	if _, err := os.Stat(configPath); err != nil {
		return nil, nil, fmt.Errorf("config %s: %w", configPath, err)
	}
	allowConfigDirFor(configPath)
	cfg, err := ares_config.Load(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config %s: %w", configPath, err)
	}
	if !cfg.Storage.Enabled || cfg.Storage.Type != storageTypePostgresCLI || cfg.Storage.Host == "" {
		return nil, nil, errors.New(
			"the AKG export/import surface needs the persistent Postgres store " +
				"(storage.enabled: true, storage.type: postgres, storage.host set); " +
				"the default in-memory store lives inside the serve process and holds " +
				"nothing a separate command can read")
	}

	db, err := sql.Open("pgx", (&postgres.Config{
		Host:     cfg.Storage.Host,
		Port:     cfg.Storage.Port,
		User:     cfg.Storage.Username,
		Password: cfg.Storage.Password,
		Database: cfg.Storage.Database,
		SSLMode:  cfg.Storage.SSLMode,
	}).DSN())
	if err != nil {
		return nil, nil, fmt.Errorf("open knowledge store: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("ping knowledge store: %w", err)
	}

	store, err := postgresstore.New(db)
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("init knowledge store: %w", err)
	}
	return store, func() { _ = db.Close() }, nil
}

// storageTypePostgresCLI mirrors the bootstrap wiring's storage type literal;
// the CLI cannot reach that package-private constant.
const storageTypePostgresCLI = "postgres"

// snapshotReader resolves the snapshot input: a file path, or stdin for "-".
func snapshotReader(path string) (io.Reader, func(), error) {
	if path == "" || path == "-" {
		return os.Stdin, func() {}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open snapshot %s: %w", path, err)
	}
	return f, func() { _ = f.Close() }, nil
}

// snapshotCounts reports what one snapshot operation moved.
type snapshotCounts struct {
	Objects         int
	Representations int
	Namespace       string
}

// exportSnapshotTo writes the snapshot for namespace to w and returns its
// counts. It writes the whole document before reporting, so a failure leaves
// stdout un-piped rather than half a snapshot.
func exportSnapshotTo(ctx context.Context, w io.Writer, store knowledge.KnowledgeStore, namespace string) (snapshotCounts, error) {
	snapshot, err := knowledge.ExportStore(ctx, store, namespace)
	if err != nil {
		return snapshotCounts{}, fmt.Errorf("export: %w", err)
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return snapshotCounts{}, fmt.Errorf("encode snapshot: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return snapshotCounts{}, fmt.Errorf("write snapshot: %w", err)
	}
	return snapshotCounts{
		Objects:         len(snapshot.Objects),
		Representations: len(snapshot.Representations),
		Namespace:       snapshot.Namespace,
	}, nil
}

// importSnapshotFrom decodes a snapshot from r and persists it.
func importSnapshotFrom(ctx context.Context, r io.Reader, store knowledge.KnowledgeStore) (snapshotCounts, error) {
	decoder := json.NewDecoder(r)
	var snapshot knowledge.StoreSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return snapshotCounts{}, fmt.Errorf("decode snapshot: %w", err)
	}
	if len(snapshot.Objects) == 0 {
		// An empty snapshot is almost always the wrong file, and importing it
		// "successfully" would report a restore that restored nothing.
		return snapshotCounts{}, errors.New("snapshot holds no objects; refusing to import nothing")
	}
	if err := knowledge.ImportStore(ctx, store, &snapshot); err != nil {
		return snapshotCounts{}, fmt.Errorf("import: %w", err)
	}
	return snapshotCounts{
		Objects:         len(snapshot.Objects),
		Representations: len(snapshot.Representations),
		Namespace:       snapshot.Namespace,
	}, nil
}
