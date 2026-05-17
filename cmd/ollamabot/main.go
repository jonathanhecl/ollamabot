package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/capabilities"
	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/docs"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/probe"
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
	if len(remaining) == 0 {
		usage()
		return nil
	}

	cfg, err := config.Load(*envPath)
	if err != nil {
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

	switch remaining[0] {
	case "probe":
		return runProbe(ctx, remaining[1:], cfg, runner)
	case "docs":
		return runDocs(ctx, remaining[1:], cfg, client, runner)
	default:
		usage()
		return fmt.Errorf("unknown command %q", remaining[0])
	}
}

func runProbe(ctx context.Context, args []string, cfg config.Config, runner *probe.Runner) error {
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
	case "chat", "tools", "json", "thinking", "embeddings", "audio":
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
		case "audio":
			result, err = runner.Audio(ctx, *model)
		}
		printResult(result)
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

func printResult(result probe.Result) {
	fmt.Printf("%s\t%s\t%s\t%s\n", result.Name, result.Model, result.Status, strings.TrimSpace(result.Details))
}

func usage() {
	fmt.Println("usage: ollamabot [--env .env] [--base-url URL] <probe|docs> ...")
	probeUsage()
	fmt.Println("docs:")
	fmt.Println("  docs generate [--out docs]")
}

func probeUsage() {
	fmt.Println("probes:")
	fmt.Println("  probe models")
	fmt.Println("  probe chat --model MODEL")
	fmt.Println("  probe tools --model MODEL")
	fmt.Println("  probe json --model MODEL")
	fmt.Println("  probe vision --model MODEL --image PATH")
	fmt.Println("  probe thinking --model MODEL")
	fmt.Println("  probe embeddings --model MODEL")
	fmt.Println("  probe audio --model MODEL")
}
