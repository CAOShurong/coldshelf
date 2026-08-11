package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/CAOShurong/coldshelf/internal/catalog"
	"github.com/CAOShurong/coldshelf/internal/importer"
	"github.com/CAOShurong/coldshelf/internal/label"
	"github.com/CAOShurong/coldshelf/internal/scanner"
	"github.com/CAOShurong/coldshelf/internal/server"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "coldshelf:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	args = normalizeCommandArgs(args)
	switch args[0] {
	case "serve":
		return serveCommand(args[1:], stdout, stderr)
	case "scan":
		return scanCommand(args[1:], stdout, stderr)
	case "search":
		return searchCommand(args[1:], stdout, stderr)
	case "drives":
		return drivesCommand(args[1:], stdout, stderr)
	case "diff":
		return diffCommand(args[1:], stdout, stderr)
	case "label":
		return labelCommand(args[1:], stdout, stderr)
	case "export":
		return exportCommand(args[1:], stdout, stderr)
	case "import-efu":
		return importEFUCommand(args[1:], stdout, stderr)
	case "demo":
		return demoCommand(args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "ColdShelf %s (%s, %s)\n", version, commit, date)
		return nil
	case "help", "--help", "-h":
		printUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q; run 'coldshelf help'", args[0])
	}
}

func normalizeCommandArgs(args []string) []string {
	if len(args) == 0 {
		return []string{"serve", "--open"}
	}
	return args
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `ColdShelf — know which unplugged drive holds your file

Usage:
  coldshelf serve [--db PATH] [--listen 127.0.0.1:4877] [--open]
  coldshelf scan PATH [--name NAME] [--drive ID] [--hash none|quick|full]
  coldshelf search QUERY [--drive ID] [--json]
  coldshelf drives [--json]
  coldshelf diff DRIVE --from SNAPSHOT --to SNAPSHOT [--json]
  coldshelf label DRIVE [--out label.svg]
  coldshelf export [--format json|csv] [--out FILE]
  coldshelf import-efu FILE --name NAME [--strip-prefix PATH]
  coldshelf demo [--db FILE] [--serve] [--open]
  coldshelf version

Run "coldshelf COMMAND --help" for command-specific options.`)
}

type serveOptions struct {
	dbPath      string
	listen      string
	openBrowser bool
}

func parseServeOptions(args []string, stderr io.Writer) (serveOptions, error) {
	defaults, err := server.DefaultCatalogPath()
	if err != nil {
		return serveOptions{}, err
	}
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", defaults, "catalog database path")
	listen := flags.String("listen", "127.0.0.1:4877", "loopback address")
	openBrowserFlag := flags.Bool("open", false, "open the browser")
	if err := flags.Parse(args); err != nil {
		return serveOptions{}, err
	}
	return serveOptions{
		dbPath:      *dbPath,
		listen:      *listen,
		openBrowser: *openBrowserFlag,
	}, nil
}

func serveCommand(args []string, stdout, stderr io.Writer) error {
	options, err := parseServeOptions(args, stderr)
	if err != nil {
		return err
	}
	c, err := catalog.Open(options.dbPath)
	if err != nil {
		return err
	}
	defer c.Close()
	srv, err := server.New(c, version)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	url := "http://" + options.listen
	fmt.Fprintf(stdout, "ColdShelf %s\nCatalog: %s\nOpen:    %s\n", version, c.Path(), url)
	openBrowserIfRequested(options.openBrowser, url, stderr, openBrowser, 250*time.Millisecond)
	return srv.ListenAndServe(ctx, options.listen)
}

func scanCommand(args []string, stdout, stderr io.Writer) error {
	defaults, _ := server.DefaultCatalogPath()
	flags := flag.NewFlagSet("scan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", defaults, "catalog database path")
	name := flags.String("name", "", "name for a new drive")
	driveRef := flags.String("drive", "", "existing drive ID or name to rescan")
	location := flags.String("location", "", "physical location, such as 'blue case · shelf B'")
	notes := flags.String("notes", "", "free-form notes")
	tags := flags.String("tags", "", "comma-separated tags")
	hashMode := flags.String("hash", "none", "none, quick, or full")
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	var excludes stringListFlag
	flags.Var(&excludes, "exclude", "glob to exclude; repeatable")
	if err := flags.Parse(interspersed(args, "json")); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("scan requires exactly one mounted directory path")
	}
	mode := scanner.HashMode(*hashMode)
	if mode != scanner.HashNone && mode != scanner.HashQuick && mode != scanner.HashFull {
		return errors.New("--hash must be none, quick, or full")
	}
	root, err := filepath.Abs(flags.Arg(0))
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("open scan path: %w", err)
	}
	if !info.IsDir() {
		return errors.New("scan path must be a directory or mounted volume")
	}
	c, err := catalog.Open(*dbPath)
	if err != nil {
		return err
	}
	defer c.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var drive catalog.Drive
	if *driveRef != "" {
		drive, err = c.ResolveDrive(ctx, *driveRef)
	} else {
		driveName := strings.TrimSpace(*name)
		if driveName == "" {
			driveName = filepath.Base(root)
		}
		drive, err = c.CreateDrive(ctx, catalog.NewDrive{
			Name: driveName, SourcePath: root, Location: *location, Notes: *notes, Tags: splitTags(*tags),
		})
	}
	if err != nil {
		return err
	}
	writer, err := c.StartSnapshot(ctx, drive.ID, root, string(mode))
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			_ = writer.Fail(errors.New("scan stopped before completion"))
		}
	}()
	started := time.Now()
	result, err := scanner.Scan(ctx, root, scanner.Options{HashMode: mode, Exclude: excludes}, writer.Add,
		func(progress scanner.Progress) {
			if !*jsonOutput {
				fmt.Fprintf(stderr, "\rIndexed %d files · %s", progress.Files, humanBytes(progress.Bytes))
			}
		},
		func(scanPath string, scanErr error) {
			writer.AddError()
			if !*jsonOutput {
				fmt.Fprintf(stderr, "\nSkipped %s: %v\n", scanPath, scanErr)
			}
		},
	)
	if !*jsonOutput {
		fmt.Fprintln(stderr)
	}
	if err != nil {
		_ = writer.Fail(err)
		return err
	}
	snapshot, err := writer.Complete()
	if err != nil {
		return err
	}
	completed = true
	if *jsonOutput {
		return json.NewEncoder(stdout).Encode(struct {
			Drive    catalog.Drive    `json:"drive"`
			Snapshot catalog.Snapshot `json:"snapshot"`
			Duration string           `json:"duration"`
		}{drive, snapshot, time.Since(started).Round(time.Millisecond).String()})
	}
	fmt.Fprintf(stdout, "Cataloged %s\n  ID:         %s\n  Snapshot:   %d\n  Files:      %d\n  Folders:    %d\n  Size:       %s\n  Read errors: %d\n  Duration:   %s\n",
		drive.Name, drive.ID, snapshot.ID, result.Files, result.Directories, humanBytes(result.Bytes), result.Errors, result.Duration.Round(time.Millisecond))
	return nil
}

func searchCommand(args []string, stdout, stderr io.Writer) error {
	defaults, _ := server.DefaultCatalogPath()
	flags := flag.NewFlagSet("search", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", defaults, "catalog database path")
	drive := flags.String("drive", "", "limit search to one drive ID")
	limit := flags.Int("limit", 100, "maximum results")
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	if err := flags.Parse(interspersed(args, "json")); err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if query == "" {
		return errors.New("search requires a query")
	}
	c, err := catalog.Open(*dbPath)
	if err != nil {
		return err
	}
	defer c.Close()
	hits, err := c.Search(context.Background(), query, *drive, *limit)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(stdout).Encode(hits)
	}
	for _, hit := range hits {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", hit.DriveName, humanBytes(hit.Size), hit.Kind, hit.Path)
	}
	if len(hits) == 0 {
		fmt.Fprintln(stdout, "No matches.")
	}
	return nil
}

func drivesCommand(args []string, stdout, stderr io.Writer) error {
	defaults, _ := server.DefaultCatalogPath()
	flags := flag.NewFlagSet("drives", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", defaults, "catalog database path")
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	c, err := catalog.Open(*dbPath)
	if err != nil {
		return err
	}
	defer c.Close()
	drives, err := c.ListDrives(context.Background())
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(stdout).Encode(drives)
	}
	for _, drive := range drives {
		fmt.Fprintf(stdout, "%s\t%s\t%d files\t%s\t%s\n", drive.ID, drive.Name, drive.FileCount, humanBytes(drive.TotalBytes), drive.Location)
	}
	if len(drives) == 0 {
		fmt.Fprintln(stdout, "No drives cataloged. Run 'coldshelf scan PATH --name NAME'.")
	}
	return nil
}

func diffCommand(args []string, stdout, stderr io.Writer) error {
	defaults, _ := server.DefaultCatalogPath()
	flags := flag.NewFlagSet("diff", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", defaults, "catalog database path")
	from := flags.Int64("from", 0, "older snapshot ID")
	to := flags.Int64("to", 0, "newer snapshot ID")
	limit := flags.Int("limit", 1000, "maximum changes")
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	if err := flags.Parse(interspersed(args, "json")); err != nil {
		return err
	}
	if flags.NArg() != 1 || *from == 0 || *to == 0 {
		return errors.New("usage: coldshelf diff DRIVE --from SNAPSHOT --to SNAPSHOT")
	}
	c, err := catalog.Open(*dbPath)
	if err != nil {
		return err
	}
	defer c.Close()
	drive, err := c.ResolveDrive(context.Background(), flags.Arg(0))
	if err != nil {
		return err
	}
	changes, err := c.Diff(context.Background(), drive.ID, *from, *to, *limit)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(stdout).Encode(changes)
	}
	for _, change := range changes {
		fmt.Fprintf(stdout, "%-8s %s\n", change.Change, change.Path)
	}
	if len(changes) == 0 {
		fmt.Fprintln(stdout, "No file-level changes.")
	}
	return nil
}

func labelCommand(args []string, stdout, stderr io.Writer) error {
	defaults, _ := server.DefaultCatalogPath()
	flags := flag.NewFlagSet("label", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", defaults, "catalog database path")
	outPath := flags.String("out", "", "output SVG path")
	if err := flags.Parse(interspersed(args)); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("label requires a drive ID or name")
	}
	c, err := catalog.Open(*dbPath)
	if err != nil {
		return err
	}
	defer c.Close()
	drive, err := c.ResolveDrive(context.Background(), flags.Arg(0))
	if err != nil {
		return err
	}
	value, err := label.SVG(drive)
	if err != nil {
		return err
	}
	if *outPath == "" {
		*outPath = "coldshelf-" + drive.ID + ".svg"
	}
	if err := os.WriteFile(*outPath, value, 0o644); err != nil {
		return err
	}
	abs, _ := filepath.Abs(*outPath)
	fmt.Fprintln(stdout, abs)
	return nil
}

func exportCommand(args []string, stdout, stderr io.Writer) error {
	defaults, _ := server.DefaultCatalogPath()
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", defaults, "catalog database path")
	format := flags.String("format", "json", "json or csv")
	outPath := flags.String("out", "-", "output path or - for stdout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	c, err := catalog.Open(*dbPath)
	if err != nil {
		return err
	}
	defer c.Close()
	var output io.Writer = stdout
	var file *os.File
	if *outPath != "-" {
		file, err = os.Create(*outPath)
		if err != nil {
			return err
		}
		defer file.Close()
		output = file
	}
	switch strings.ToLower(*format) {
	case "json":
		return c.ExportJSON(context.Background(), output)
	case "csv":
		return c.ExportCSV(context.Background(), output)
	default:
		return errors.New("--format must be json or csv")
	}
}

func importEFUCommand(args []string, stdout, stderr io.Writer) error {
	defaults, _ := server.DefaultCatalogPath()
	flags := flag.NewFlagSet("import-efu", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", defaults, "catalog database path")
	name := flags.String("name", "", "name for the imported drive")
	location := flags.String("location", "", "physical drive location")
	stripPrefix := flags.String("strip-prefix", "", "path prefix to remove from EFU entries")
	if err := flags.Parse(interspersed(args)); err != nil {
		return err
	}
	if flags.NArg() != 1 || strings.TrimSpace(*name) == "" {
		return errors.New("usage: coldshelf import-efu FILE --name NAME")
	}
	file, err := os.Open(flags.Arg(0))
	if err != nil {
		return err
	}
	defer file.Close()
	c, err := catalog.Open(*dbPath)
	if err != nil {
		return err
	}
	defer c.Close()
	drive, err := c.CreateDrive(context.Background(), catalog.NewDrive{Name: *name, SourcePath: *stripPrefix, Location: *location, Tags: []string{"efu-import"}})
	if err != nil {
		return err
	}
	writer, err := c.StartSnapshot(context.Background(), drive.ID, *stripPrefix, "imported")
	if err != nil {
		return err
	}
	result, err := importer.EFU(file, *stripPrefix, writer.Add)
	if err != nil {
		_ = writer.Fail(err)
		return err
	}
	snapshot, err := writer.Complete()
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Imported %d files and %d folders (%s) into %s · snapshot %d\n", result.Files, result.Directories, humanBytes(result.Bytes), drive.Name, snapshot.ID)
	return nil
}

type demoOptions struct {
	dbPath      string
	listen      string
	serve       bool
	openBrowser bool
}

func parseDemoOptions(args []string, stderr io.Writer) (demoOptions, error) {
	flags := flag.NewFlagSet("demo", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "coldshelf-demo.db", "demo catalog path")
	serve := flags.Bool("serve", false, "serve the demo after creating it")
	listen := flags.String("listen", "127.0.0.1:4877", "loopback address")
	openBrowserFlag := flags.Bool("open", false, "open the browser; requires --serve")
	if err := flags.Parse(args); err != nil {
		return demoOptions{}, err
	}
	if *openBrowserFlag && !*serve {
		return demoOptions{}, errors.New("--open requires --serve")
	}
	return demoOptions{
		dbPath:      *dbPath,
		listen:      *listen,
		serve:       *serve,
		openBrowser: *openBrowserFlag,
	}, nil
}

func demoCommand(args []string, stdout, stderr io.Writer) error {
	options, err := parseDemoOptions(args, stderr)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(options.dbPath)
	if err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(abs + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("reset demo catalog: %w", err)
		}
	}
	c, err := catalog.Open(abs)
	if err != nil {
		return err
	}
	if err := seedDemo(c); err != nil {
		c.Close()
		return err
	}
	fmt.Fprintf(stdout, "Demo catalog created: %s\n", abs)
	if !options.serve {
		return c.Close()
	}
	srv, err := server.New(c, version)
	if err != nil {
		c.Close()
		return err
	}
	defer c.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	openBrowserIfRequested(options.openBrowser, "http://"+options.listen, stderr, openBrowser, 250*time.Millisecond)
	return srv.ListenAndServe(ctx, options.listen)
}

func seedDemo(c *catalog.Catalog) error {
	type demoDrive struct {
		name, source, location string
		tags                   []string
		groups                 []demoGroup
	}
	drives := []demoDrive{
		{"Amber Archive", `E:\\AMBER_ARCHIVE`, "Blue case · shelf B", []string{"client-work", "video", "2019–2024"}, []demoGroup{{"Projects/Aurora/Camera", "AURORA_A_CAM_%04d.mov", 168, 11_800_000_000}, {"Projects/NeonHarbor/Exports", "NH_MASTER_%03d.mov", 42, 24_200_000_000}, {"Audio/Field", "FIELD_%04d.wav", 320, 94_000_000}, {"Documents/Invoices", "invoice_%04d.pdf", 760, 420_000}}},
		{"Silver Photos", `F:\\SILVER_PHOTOS`, "Fireproof box · slot 03", []string{"photography", "raw", "family"}, []demoGroup{{"Photos/2025/Iceland/RAW", "DSC_%05d.ARW", 1280, 48_000_000}, {"Photos/2024/Family", "IMG_%05d.CR3", 940, 36_000_000}, {"Lightroom/Previews", "preview_%05d.jpg", 2100, 1_200_000}}},
		{"Field Backup 07", `/Volumes/FIELD_BACKUP_07`, "Black Pelican · bay 7", []string{"field", "backup", "research"}, []demoGroup{{"Research/Microscopy/Raw", "sample_%04d.tif", 610, 820_000_000}, {"Research/Measurements", "sweep_%05d.csv", 3400, 580_000}, {"Recovery/Checksums", "manifest_%03d.sha256", 88, 84_000}}},
	}
	for index, input := range drives {
		drive, err := c.CreateDrive(context.Background(), catalog.NewDrive{Name: input.name, SourcePath: input.source, Location: input.location, Tags: input.tags, Notes: "Generated demonstration catalog; no source files are required."})
		if err != nil {
			return err
		}
		entries := generateDemoEntries(input.groups, index)
		if index == 0 {
			old := append([]catalog.Entry(nil), entries...)
			old = old[:len(old)-14]
			if err := writeDemoSnapshot(c, drive.ID, input.source, old, time.Now().AddDate(0, -2, 0)); err != nil {
				return err
			}
		}
		if err := writeDemoSnapshot(c, drive.ID, input.source, entries, time.Now().Add(-time.Duration(index+1)*22*time.Hour)); err != nil {
			return err
		}
	}
	return nil
}

type demoGroup struct {
	directory, pattern string
	count              int
	size               int64
}

func generateDemoEntries(groups []demoGroup, driveIndex int) []catalog.Entry {
	seenDirs := make(map[string]bool)
	entries := make([]catalog.Entry, 0)
	baseTime := time.Date(2026, time.July, 15-driveIndex, 14, 20, 0, 0, time.UTC)
	for groupIndex, group := range groups {
		parts := strings.Split(group.directory, "/")
		for partIndex := range parts {
			dir := strings.Join(parts[:partIndex+1], "/")
			if seenDirs[dir] {
				continue
			}
			seenDirs[dir] = true
			parent := ""
			if partIndex > 0 {
				parent = strings.Join(parts[:partIndex], "/")
			}
			entries = append(entries, catalog.Entry{Path: dir, ParentPath: parent, Name: parts[partIndex], Kind: "directory", ModifiedAt: baseTime})
		}
		for i := 1; i <= group.count; i++ {
			name := fmt.Sprintf(group.pattern, i)
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
			size := group.size + int64((i*7919+groupIndex*173)%900_000)
			hash := ""
			if i == 1 && groupIndex == 0 && driveIndex < 2 {
				hash = "sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
				size = 42_424_242
			}
			entries = append(entries, catalog.Entry{Path: group.directory + "/" + name, ParentPath: group.directory, Name: name, Extension: ext, Kind: "file", Size: size, ModifiedAt: baseTime.Add(-time.Duration(i) * time.Hour), Hash: hash})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}

func writeDemoSnapshot(c *catalog.Catalog, driveID, source string, entries []catalog.Entry, _ time.Time) error {
	writer, err := c.StartSnapshot(context.Background(), driveID, source, "demo")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := writer.Add(entry); err != nil {
			_ = writer.Fail(err)
			return err
		}
	}
	_, err = writer.Complete()
	return err
}

func splitTags(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}

type stringListFlag []string

func (s *stringListFlag) String() string         { return strings.Join(*s, ",") }
func (s *stringListFlag) Set(value string) error { *s = append(*s, value); return nil }

func interspersed(args []string, booleanFlags ...string) []string {
	booleans := make(map[string]bool, len(booleanFlags))
	for _, name := range booleanFlags {
		booleans[name] = true
	}
	options := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		value := args[index]
		if value == "--" {
			positionals = append(positionals, args[index+1:]...)
			break
		}
		if !strings.HasPrefix(value, "-") || value == "-" {
			positionals = append(positionals, value)
			continue
		}
		options = append(options, value)
		name := strings.TrimLeft(strings.SplitN(value, "=", 2)[0], "-")
		if strings.Contains(value, "=") || booleans[name] {
			continue
		}
		if index+1 < len(args) {
			index++
			options = append(options, args[index])
		}
	}
	return append(options, positionals...)
}

func humanBytes(value int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	amount := float64(value)
	unit := 0
	for amount >= 1000 && unit < len(units)-1 {
		amount /= 1000
		unit++
	}
	if unit == 0 {
		return strconv.FormatInt(value, 10) + " " + units[unit]
	}
	return fmt.Sprintf("%.2f %s", amount, units[unit])
}

func openBrowserIfRequested(enabled bool, url string, stderr io.Writer, opener func(string) error, delay time.Duration) {
	if !enabled {
		return
	}
	go func() {
		time.Sleep(delay)
		if err := opener(url); err != nil {
			fmt.Fprintln(stderr, "Could not open browser:", err)
		}
	}()
}

func openBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		command = exec.Command("open", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	return command.Start()
}
