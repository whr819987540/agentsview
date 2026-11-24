package main

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"go.kenn.io/agentsview/internal/db"
	"go.kenn.io/agentsview/internal/pathutil"
)

// curationListLimit caps how many unmatched or ambiguous session IDs the
// human import report prints; --json lists them all.
const curationListLimit = 10

func newCurationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "curation",
		Short: "Move stars, end marks, and folders to another archive",
		Long: "Move stars, end marks, and session folders to another archive, " +
			"such as when sessions move to a new machine. Export them where " +
			"you curated the sessions, copy the file, and import it where the " +
			"sessions are now.",
		GroupID:      groupData,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newCurationExportCommand())
	cmd.AddCommand(newCurationImportCommand())
	return cmd
}

func newCurationExportCommand() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write stars, end marks, and folders as JSON",
		Long: "Write this archive's stars, end marks still in effect, and " +
			"session folders as one JSON document, to standard output or " +
			"to the file named by --output.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cleanup, err := resolveService(cmd)
			if err != nil {
				return err
			}
			defer cleanup()
			doc, err := svc.ExportCuration(cmd.Context())
			if err != nil {
				return curationServiceError("export", err)
			}
			if output == "" || output == "-" {
				return writeCurationExport(cmd.OutOrStdout(), doc)
			}
			path, err := pathutil.ExpandHome(output)
			if err != nil {
				return fmt.Errorf("expanding --output: %w", err)
			}
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
			if err != nil {
				return fmt.Errorf("creating curation export: %w", err)
			}
			if err := writeCurationExport(f, doc); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("writing curation export: %w", err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Exported %d curated %s and %d folder%s to %s\n",
				len(doc.Sessions), pluralSession(len(doc.Sessions)),
				len(doc.Folders), pluralSuffix(len(doc.Folders)), path)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "",
		"Write the export to this file instead of standard output")
	return cmd
}

func writeCurationExport(w io.Writer, doc *db.CurationExport) error {
	if err := json.MarshalWrite(w, doc, jsontext.WithIndent("  ")); err != nil {
		return fmt.Errorf("writing curation export: %w", err)
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return fmt.Errorf("writing curation export: %w", err)
	}
	return nil
}

func newCurationImportCommand() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Apply exported stars, end marks, and folders",
		Long: "Apply a curation export to the matching sessions of this " +
			"archive. A session matches by ID, or by the one session whose ID " +
			"ends in \"~\" plus that ID, as artifact sync and remote sync name " +
			"the copies they import; its agent must match too. The import " +
			"adds what each session lacks and keeps every star, end mark, and " +
			"folder already here. Folders are recreated in the project each " +
			"session has here. Pass - to read standard input.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			doc, err := readCurationExport(cmd.InOrStdin(), args[0])
			if err != nil {
				return err
			}
			svc, cleanup, err := resolveWritableService(cmd)
			if err != nil {
				return err
			}
			defer cleanup()
			result, err := svc.ImportCuration(cmd.Context(), doc,
				db.CurationImportOptions{DryRun: dryRun})
			if err != nil {
				return curationServiceError("import", err)
			}
			if outputFormat(cmd) == "json" {
				return json.MarshalEncode(jsontext.NewEncoder(cmd.OutOrStdout()), result)
			}
			printCurationImportResult(cmd.OutOrStdout(), result)
			return nil
		},
	}
	registerFormatFlags(cmd.Flags())
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Report what the import would change without writing")
	return cmd
}

// readCurationExport reads and checks a curation export before any daemon
// or archive is opened, so a wrong file fails fast.
func readCurationExport(stdin io.Reader, name string) (db.CurationExport, error) {
	r := stdin
	if name != "-" {
		path, err := pathutil.ExpandHome(name)
		if err != nil {
			return db.CurationExport{}, fmt.Errorf("expanding curation export path: %w", err)
		}
		f, err := os.Open(path)
		if err != nil {
			return db.CurationExport{}, fmt.Errorf("opening curation export: %w", err)
		}
		defer f.Close()
		r = f
	}
	var doc db.CurationExport
	if err := json.UnmarshalRead(r, &doc); err != nil {
		return db.CurationExport{}, fmt.Errorf("reading curation export: %w", err)
	}
	if err := db.ValidateCurationExport(&doc); err != nil {
		return db.CurationExport{}, err
	}
	return doc, nil
}

func curationServiceError(action string, err error) error {
	if errors.Is(err, db.ErrReadOnly) {
		return fmt.Errorf("curation %s needs the local SQLite archive: %w", action, err)
	}
	return err
}

func printCurationImportResult(w io.Writer, r *db.CurationImportResult) {
	fmt.Fprintf(w, "Matched %d of %d %s", r.Matched, r.Sessions, pluralSession(r.Sessions))
	if r.MatchedByPrefix > 0 {
		fmt.Fprintf(w, " (%d through a prefixed ID)", r.MatchedByPrefix)
	}
	fmt.Fprintln(w, ".")
	printCurationCounts(w, "Stars", r.Stars)
	printCurationCounts(w, "End marks", r.EndMarks)
	printCurationCounts(w, "Folder members", r.FolderMembers)
	fmt.Fprintf(w, "%-16s %d\n", "Folders created:", r.FoldersCreated)
	printCurationIDs(w, "Not found here", r.Unmatched)
	printCurationIDs(w, "Matched more than one session", r.Ambiguous)
	if r.DryRun {
		fmt.Fprintln(w, "Dry run: nothing was written.")
	}
}

func printCurationCounts(w io.Writer, label string, c db.CurationImportCounts) {
	fmt.Fprintf(w, "%-16s added %d, already here %d, kept existing %d\n",
		label+":", c.Added, c.Unchanged, c.Kept)
}

func printCurationIDs(w io.Writer, label string, ids []string) {
	if len(ids) == 0 {
		return
	}
	shown := ids[:min(len(ids), curationListLimit)]
	fmt.Fprintf(w, "%s (%d): %s", label, len(ids), strings.Join(shown, ", "))
	if more := len(ids) - len(shown); more > 0 {
		fmt.Fprintf(w, ", and %d more (use --json to list all)", more)
	}
	fmt.Fprintln(w)
}
