package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/agent"
	"github.com/jonathanhecl/ollamabot/internal/cache"
	"github.com/jonathanhecl/ollamabot/internal/capabilities"
	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/docs"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/probe"
	"github.com/jonathanhecl/ollamabot/internal/web"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	global := flag.NewFlagSet("ollamabot", flag.ContinueOnError)
	envPath := global.String("env", ".env", "path to .env file")
	baseURL := global.String("base-url", "", "override Ollama base URL")
	if err := global.Parse(args); err != nil {
		return err
	}
	remaining := global.Args()
	if len(remaining) == 0 && !config.Exists(*envPath) {
		fmt.Printf("Could not find %s. Let's create it with basic configuration.\n", *envPath)
		if err := config.CreateInteractive(*envPath, os.Stdin, os.Stdout); err != nil {
			return err
		}
		fmt.Printf("Done, saved %s.\n", *envPath)
	}

	cfg, err := config.Load(*envPath)
	if err != nil {
		return err
	}
	cfg.Workspace, err = resolveWorkspace(cfg.Workspace)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.Workspace, 0o755); err != nil {
		return err
	}
	cfg.SessionsPath, err = resolveWorkspace(cfg.SessionsPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.SessionsPath, 0o755); err != nil {
		return err
	}
	cfg.MemoryPath, err = resolveWorkspace(cfg.MemoryPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.MemoryPath, 0o755); err != nil {
		return err
	}
	cfg.SkillsPath, err = resolveWorkspace(cfg.SkillsPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.SkillsPath, 0o755); err != nil {
		return err
	}
	if err := agent.EnsureSoulDirAndFile(); err != nil {
		return err
	}
	if *baseURL != "" {
		normalized, err := config.NormalizeBaseURL(*baseURL)
		if err != nil {
			return err
		}
		cfg.OllamaBaseURL = normalized
	}

	client := ollama.NewClient(cfg.OllamaBaseURL)
	runner := probe.NewRunner(client)
	ctx := context.Background()

	if len(remaining) == 0 {
		if cfg.ServerEnabled {
			fmt.Printf("OllamaBot web: http://localhost:%s\n", cfg.ServerPort)
			return web.NewServerWithEnv(cfg, client, runner, web.SnapshotPath(""), *envPath).ListenAndServe()
		}
		fmt.Println("Server disabled in .env (SERVER_ENABLED=false).")
		usage()
		return nil
	}

	switch remaining[0] {
	case "probe":
		return runProbe(ctx, remaining[1:], cfg, client, runner, web.SnapshotPath(""))
	case "docs":
		return runDocs(ctx, remaining[1:], cfg, client, runner)
	case "serve":
		return runServe(remaining[1:], cfg, client, runner, *envPath)
	default:
		usage()
		return fmt.Errorf("unknown command %q", remaining[0])
	}
}

func runProbe(ctx context.Context, args []string, cfg config.Config, client *ollama.Client, runner *probe.Runner, cachePath string) error {
	if len(args) == 0 {
		probeUsage()
		return nil
	}
	switch args[0] {
	case "models":
		reports, err := runner.Inventory(ctx, cfg.OllamaProbeModels)
		if err != nil {
			return err
		}
		for _, report := range reports {
			fmt.Printf("%s\t%s\t%s\n", report.Name, report.Parameters, capabilities.StatusList(report.Capabilities))
		}
		return nil
	case "snapshot":
		flags := flag.NewFlagSet("probe snapshot", flag.ContinueOnError)
		out := flags.String("out", web.SnapshotPath(""), "snapshot output path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		return writeSnapshot(ctx, *out, cfg, client, runner)
	case "chat", "tools", "json", "thinking", "embeddings":
		flags := flag.NewFlagSet("probe "+args[0], flag.ContinueOnError)
		model := flags.String("model", cfg.OllamaDefaultModel, "model name")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *model == "" {
			return fmt.Errorf("--model is required")
		}
		var (
			result probe.Result
			err    error
		)
		switch args[0] {
		case "chat":
			result, err = runner.Chat(ctx, *model)
		case "tools":
			result, err = runner.Tools(ctx, *model)
		case "json":
			result, err = runner.JSON(ctx, *model)
		case "thinking":
			result, err = runner.Thinking(ctx, *model)
		case "embeddings":
			result, err = runner.Embeddings(ctx, *model)
		}
		printResult(result)
		saveRun(cachePath, result)
		return err
	case "audio":
		flags := flag.NewFlagSet("probe audio", flag.ContinueOnError)
		model := flags.String("model", cfg.OllamaDefaultModel, "model name")
		audioPath := flags.String("audio", "", "audio file path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *model == "" {
			return fmt.Errorf("--model is required")
		}
		result, err := runner.Audio(ctx, *model, *audioPath)
		printResult(result)
		saveRun(cachePath, result)
		return err
	case "vision":
		flags := flag.NewFlagSet("probe vision", flag.ContinueOnError)
		model := flags.String("model", cfg.OllamaDefaultModel, "model name")
		imagePath := flags.String("image", "", "image path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *model == "" {
			return fmt.Errorf("--model is required")
		}
		if *imagePath == "" {
			return fmt.Errorf("--image is required")
		}
		result, err := runner.Vision(ctx, *model, *imagePath)
		printResult(result)
		saveRun(cachePath, result)
		return err
	default:
		probeUsage()
		return fmt.Errorf("unknown probe %q", args[0])
	}
}

func runDocs(ctx context.Context, args []string, cfg config.Config, client *ollama.Client, runner *probe.Runner) error {
	if len(args) == 0 || args[0] != "generate" {
		fmt.Println("usage: ollamabot docs generate [--out docs]")
		return nil
	}
	flags := flag.NewFlagSet("docs generate", flag.ContinueOnError)
	outDir := flags.String("out", "docs", "output directory")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	version, _ := client.Version(ctx)
	reports, err := runner.Inventory(ctx, cfg.OllamaProbeModels)
	if err != nil {
		return err
	}
	data := docs.ReferenceData{
		BaseURL:       cfg.OllamaBaseURL,
		OllamaVersion: version.Version,
		GeneratedAt:   time.Now(),
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}
	files := map[string]string{
		filepath.Join(*outDir, "ollama-reference.md"):      docs.Reference(data),
		filepath.Join(*outDir, "local-model-inventory.md"): docs.Inventory(reports, data),
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
		fmt.Println("wrote", path)
	}
	return nil
}

func runServe(args []string, cfg config.Config, client *ollama.Client, runner *probe.Runner, envPath string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := flags.String("port", cfg.ServerPort, "server port")
	cachePath := flags.String("cache", web.SnapshotPath(""), "probe snapshot cache path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg.ServerPort = *port
	return web.NewServerWithEnv(cfg, client, runner, *cachePath, envPath).ListenAndServe()
}

func writeSnapshot(ctx context.Context, out string, cfg config.Config, client *ollama.Client, runner *probe.Runner) error {
	version, _ := client.Version(ctx)
	reports, err := runner.Inventory(ctx, cfg.OllamaProbeModels)
	if err != nil {
		return err
	}
	ps, _ := client.Ps(ctx)
	snapshot := cache.Snapshot{
		GeneratedAt:   time.Now(),
		BaseURL:       cfg.OllamaBaseURL,
		OllamaVersion: version.Version,
		Models:        reports,
		Running:       ps.Models,
		Expected: []cache.ExpectedProbe{
			{Name: "models", Status: capabilities.Confirmed, Details: "Inventory from /api/tags and /api/show"},
			{Name: "audio", Status: capabilities.Pending, Details: "Audio remains pending unless an end-to-end REST payload is confirmed"},
			{Name: "video", Status: capabilities.Pending, Details: "Video remains pending; planned path is frame extraction plus vision"},
		},
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := cache.Save(out, snapshot); err != nil {
		return err
	}
	fmt.Println("wrote", out)
	return nil
}

func printResult(result probe.Result) {
	fmt.Printf("%s\t%s\t%s\t%s\n", result.Name, result.Model, result.Status, strings.TrimSpace(result.Details))
}

func saveRun(cachePath string, result probe.Result) {
	run := cache.ProbeRun{
		Name:    result.Name,
		Model:   result.Model,
		Status:  result.Status,
		Details: strings.TrimSpace(result.Details),
		RunAt:   time.Now(),
	}
	if err := cache.SaveProbeRun(cachePath, run); err != nil {
		log.Printf("warn: could not persist probe run: %v", err)
	}
}

func usage() {
	fmt.Println("usage: ollamabot [--env .env] [--base-url URL] <probe|docs> ...")
	probeUsage()
	fmt.Println("docs:")
	fmt.Println("  docs generate [--out docs]")
	fmt.Println("web:")
	fmt.Println("  serve [--port 8080] [--cache docs/probe-cache.json]")
}

func probeUsage() {
	fmt.Println("probes:")
	fmt.Println("  probe models")
	fmt.Println("  probe snapshot [--out docs/probe-cache.json]")
	fmt.Println("  probe chat --model MODEL")
	fmt.Println("  probe tools --model MODEL")
	fmt.Println("  probe json --model MODEL")
	fmt.Println("  probe vision --model MODEL --image PATH")
	fmt.Println("  probe thinking --model MODEL")
	fmt.Println("  probe embeddings --model MODEL")
	fmt.Println("  probe audio --model MODEL")
	fmt.Println("  probe audio --model MODEL --audio PATH")
}

func resolveWorkspace(workspace string) (string, error) {
	if filepath.IsAbs(workspace) {
		return workspace, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), workspace), nil
}
