// Command simple-ac is the mioty Application Center of simple-sc-ac.
//
//	simple-ac [-config ac.toml]              run the Application Center
//	simple-ac sc request -name lab           create key and certificate request for a Service Center
//	simple-ac sc add bundle.json             import the bundle a Service Center issued
//	simple-ac sc list                        show the configured Service Centers
//	simple-ac -version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/michaburger/simple-sc-ac/internal/ac/api"
	"github.com/michaburger/simple-sc-ac/internal/ac/node"
	"github.com/michaburger/simple-sc-ac/internal/ac/store"
	"github.com/michaburger/simple-sc-ac/internal/common/app"
	"github.com/michaburger/simple-sc-ac/internal/common/buildinfo"
	"github.com/michaburger/simple-sc-ac/internal/common/config"
	"github.com/michaburger/simple-sc-ac/internal/common/identity"
	"github.com/michaburger/simple-sc-ac/internal/common/onboard"
	"github.com/michaburger/simple-sc-ac/internal/proto/trace"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "simple-ac:", err)
		os.Exit(1)
	}
}

func loadConfig(path string) (config.AC, error) {
	cfg := config.DefaultAC()
	err := config.Load(&cfg, path, os.Environ())
	cfg.Finish()
	return cfg, err
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "sc" {
		return scCommand(args[1:])
	}
	fs := flag.NewFlagSet("simple-ac", flag.ContinueOnError)
	cfgPath := fs.String("config", os.Getenv("SSCAC_CONFIG"), "configuration file (TOML), optional")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println("simple-ac", buildinfo.Version)
		return nil
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	env, err := app.Start("ac", &cfg.Common)
	if err != nil {
		return err
	}
	log := env.Log
	st, err := store.Open(filepath.Join(cfg.DataDir, "ac.sqlite"))
	if err != nil {
		return err
	}
	defer st.Close()
	var sink trace.Sink = trace.Discard
	if cfg.TraceFile != "-" {
		_ = os.MkdirAll(filepath.Dir(cfg.TraceFile), 0o755)
		f, err := os.OpenFile(cfg.TraceFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		sink = trace.NewJSONLines(f)
	}
	n, err := node.New(node.Config{DataDir: cfg.DataDir, ACEui: env.EUI, Schemas: env.Schemas, Store: st, Log: log, Trace: sink,
		ResponseTimeout: cfg.ResponseTimeout, IdlePing: cfg.IdlePing, SwVersion: buildinfo.Version})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	if cfg.Admin.Listen != "" && cfg.Admin.Listen != "-" {
		srv := &http.Server{Addr: cfg.Admin.Listen, Handler: (&api.API{Node: n, Store: st, ACEui: env.EUI}).Handler(), ReadHeaderTimeout: 5 * time.Second}
		go func() { <-ctx.Done(); _ = srv.Close() }()
		go func() {
			log.Info("admin REST listening", "addr", cfg.Admin.Listen)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}
	log.Info("started", "version", buildinfo.Version, "serviceCenters", len(n.Clients()))
	go n.Run(ctx)
	select {
	case <-ctx.Done():
	case err := <-errc:
		return err
	}
	log.Info("stopped")
	return nil
}

func ownEUI(cfg config.AC) (identity.Result, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return identity.Result{}, err
	}
	return identity.Resolve("ac", cfg.EUI, cfg.DataDir)
}

func scCommand(args []string) error {
	usage := errors.New("usage: simple-ac sc request -name NAME | simple-ac sc add BUNDLE.json | simple-ac sc list")
	if len(args) == 0 {
		return usage
	}
	fs := flag.NewFlagSet("sc", flag.ContinueOnError)
	cfgPath := fs.String("config", os.Getenv("SSCAC_CONFIG"), "configuration file (TOML), optional")
	name := fs.String("name", "", "name for the Service Center, e.g. lab")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	id, err := ownEUI(cfg)
	if err != nil {
		return err
	}
	switch args[0] {
	case "request":
		if *name == "" {
			return errors.New("-name is required")
		}
		csr, err := node.Request(cfg.DataDir, *name, id.EUI)
		if err != nil {
			return err
		}
		fmt.Printf("Application Center EUI  %s\ncertificate request     %s\n\nGive the request and the EUI to the operator of the Service Center. They run\n  simple-sc ac add -eui %s -csr <request> ...\nand send you a bundle file back. Import it with\n  simple-ac sc add <bundle.json>\n", id.EUI, csr, id.EUI)
		return nil
	case "add":
		if fs.NArg() != 1 {
			return usage
		}
		b, err := node.Import(cfg.DataDir, fs.Arg(0), id.EUI)
		if err != nil {
			return err
		}
		fmt.Printf("Service Center %q added: %s, EUI %s\nStart or restart simple-ac to connect.\n", b.Name, b.Address, b.SCEui)
		return nil
	case "list":
		files, _ := filepath.Glob(filepath.Join(cfg.DataDir, "sc", "*", node.BundleFile))
		fmt.Printf("Application Center EUI %s\n", id.EUI)
		for _, f := range files {
			b, err := onboard.Read(f)
			if err != nil {
				fmt.Printf("  %s: %v\n", filepath.Dir(f), err)
				continue
			}
			fmt.Printf("  %-12s %-28s SC EUI %s\n", b.Name, b.Address, b.SCEui)
		}
		if len(files) == 0 {
			fmt.Println("  no Service Center yet, see: simple-ac sc request")
		}
		return nil
	}
	return usage
}
