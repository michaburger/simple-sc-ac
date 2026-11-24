// Command simple-ac is the mioty Application Center of simple-sc-ac.
//
//	simple-ac [-config ac.toml]
//	simple-ac -version
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/michaburger/simple-sc-ac/internal/common/app"
	"github.com/michaburger/simple-sc-ac/internal/common/buildinfo"
	"github.com/michaburger/simple-sc-ac/internal/common/config"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "simple-ac:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
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
	cfg := config.DefaultAC()
	if err := config.Load(&cfg, *cfgPath, os.Environ()); err != nil {
		return err
	}
	env, err := app.Start("ac", &cfg.Common)
	if err != nil {
		return err
	}
	env.Log.Info("started", "version", buildinfo.Version, "service_centers", len(cfg.ServiceCenters),
		"note", "Service Center connections follow in the next milestones")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	env.Log.Info("stopped")
	return nil
}
