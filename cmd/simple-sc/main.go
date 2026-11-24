// Command simple-sc is the mioty Service Center of simple-sc-ac.
//
//	simple-sc [-config sc.toml]              run the Service Center
//	simple-sc ca issue -name bs1 [-eui ...]  issue a certificate from the lab CA
//	simple-sc -version
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/michaburger/simple-sc-ac/internal/common/app"
	"github.com/michaburger/simple-sc-ac/internal/common/buildinfo"
	"github.com/michaburger/simple-sc-ac/internal/common/config"
	"github.com/michaburger/simple-sc-ac/internal/common/idt"
	"github.com/michaburger/simple-sc-ac/internal/common/labca"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "simple-sc:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "ca" {
		return caCommand(args[1:])
	}
	fs := flag.NewFlagSet("simple-sc", flag.ContinueOnError)
	cfgPath := fs.String("config", os.Getenv("SSCAC_CONFIG"), "configuration file (TOML), optional")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println("simple-sc", buildinfo.Version)
		return nil
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	env, err := app.Start("sc", &cfg.Common)
	if err != nil {
		return err
	}
	caDir := filepath.Join(cfg.DataDir, "ca")
	ca, created, err := labca.Init(caDir, "simple-sc lab CA "+env.EUI.String())
	if err != nil {
		return err
	}
	if created {
		env.Log.Info("lab certificate authority created", "dir", caDir)
	}
	if _, err := os.Stat(filepath.Join(caDir, "sc.pem")); os.IsNotExist(err) {
		host, _ := os.Hostname()
		if _, err := ca.Issue(caDir, labca.Request{Name: "sc", EUI: env.EUI.String(), Server: true, Hosts: []string{host, "localhost", "127.0.0.1"}}); err != nil {
			return err
		}
		env.Log.Info("server certificate issued", "file", filepath.Join(caDir, "sc.pem"))
	}
	if len(env.Schemas.Versions("bssci")) == 0 {
		env.Log.Warn("no BSSCI schema files, the BSSCI port stays closed", "schema_dir", cfg.SchemaDir)
	}
	env.Log.Info("started", "version", buildinfo.Version, "bssci", cfg.BSSCI.Listen, "scaci", cfg.SCACI.Listen,
		"note", "base station and Application Center connections follow in the next milestones")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	env.Log.Info("stopped")
	return nil
}

func loadConfig(path string) (config.SC, error) {
	cfg := config.DefaultSC()
	err := config.Load(&cfg, path, os.Environ())
	return cfg, err
}

func caCommand(args []string) error {
	if len(args) == 0 || args[0] != "issue" {
		return fmt.Errorf("usage: simple-sc ca issue -name NAME [-eui EUI] [-server -host H,...] [-out DIR]")
	}
	fs := flag.NewFlagSet("ca issue", flag.ContinueOnError)
	cfgPath := fs.String("config", os.Getenv("SSCAC_CONFIG"), "configuration file (TOML), optional")
	name := fs.String("name", "", "certificate name, e.g. bs-1 or ac-lab")
	eui := fs.String("eui", "", "EUI-64 of the peer, e.g. 70-B3-D5-9C-D0-00-09-BB")
	server := fs.Bool("server", false, "server certificate instead of client certificate")
	hosts := fs.String("host", "", "comma separated host names or addresses (server certificates)")
	out := fs.String("out", "", "output directory, default <data_dir>/ca/issued")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *eui != "" {
		if _, err := idt.ParseEUI64(*eui); err != nil {
			return err
		}
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	cfg.Finish()
	caDir := filepath.Join(cfg.DataDir, "ca")
	ca, _, err := labca.Init(caDir, "simple-sc lab CA")
	if err != nil {
		return err
	}
	dir := *out
	if dir == "" {
		dir = filepath.Join(caDir, "issued")
	}
	var hl []string
	if *hosts != "" {
		hl = strings.Split(*hosts, ",")
	}
	fp, err := ca.Issue(dir, labca.Request{Name: *name, EUI: *eui, Server: *server, Hosts: hl})
	if err != nil {
		return err
	}
	fmt.Printf("certificate %s\nkey         %s\nCA          %s\nfingerprint %s\n",
		filepath.Join(dir, *name+".pem"), filepath.Join(dir, *name+".key"), filepath.Join(caDir, labca.CACert), fp)
	return nil
}
