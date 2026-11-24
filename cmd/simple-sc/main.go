// Command simple-sc is the mioty Service Center of simple-sc-ac.
//
//	simple-sc [-config sc.toml]              run the Service Center
//	simple-sc ca issue -name bs1 [-eui ...]  issue a certificate from the lab CA
//	simple-sc -version
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/michaburger/simple-sc-ac/internal/common/app"
	"github.com/michaburger/simple-sc-ac/internal/common/buildinfo"
	"github.com/michaburger/simple-sc-ac/internal/common/config"
	"github.com/michaburger/simple-sc-ac/internal/common/idt"
	"github.com/michaburger/simple-sc-ac/internal/common/labca"
	"github.com/michaburger/simple-sc-ac/internal/proto/engine"
	"github.com/michaburger/simple-sc-ac/internal/proto/trace"
	"github.com/michaburger/simple-sc-ac/internal/proto/version"
	"github.com/michaburger/simple-sc-ac/internal/sc/api"
	"github.com/michaburger/simple-sc-ac/internal/sc/bssci"
	"github.com/michaburger/simple-sc-ac/internal/sc/registry"
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
		hosts := []string{host, "localhost", "127.0.0.1"}
		if app := os.Getenv("FLY_APP_NAME"); app != "" {
			hosts = append(hosts, app+".fly.dev")
		}
		for _, h := range strings.Split(cfg.BSSCI.CertHosts, ",") {
			if h = strings.TrimSpace(h); h != "" {
				hosts = append(hosts, h)
			}
		}
		if _, err := ca.Issue(caDir, labca.Request{Name: "sc", EUI: env.EUI.String(), Server: true, Hosts: hosts}); err != nil {
			return err
		}
		env.Log.Info("server certificate issued", "file", filepath.Join(caDir, "sc.pem"), "hosts", hosts)
	}
	return serve(cfg, env, caDir)
}

func serve(cfg config.SC, env *app.Env, caDir string) error {
	log := env.Log
	reg, err := registry.Open(filepath.Join(cfg.DataDir, "sc.sqlite"))
	if err != nil {
		return err
	}
	defer reg.Close()
	var sink trace.Sink = trace.Discard
	if cfg.TraceFile != "-" {
		if err := os.MkdirAll(filepath.Dir(cfg.TraceFile), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(cfg.TraceFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		sink = trace.NewJSONLines(f)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 2)

	var bs *bssci.Server
	if len(env.Schemas.Versions(bssci.Interface)) == 0 {
		log.Warn("no BSSCI schema files, the BSSCI port stays closed", "schema_dir", cfg.SchemaDir)
	} else {
		tlsCfg, err := serverTLS(cfg.BSSCI, caDir)
		if err != nil {
			return err
		}
		policy, err := version.ParsePolicy(cfg.BSSCI.Policy)
		if err != nil {
			return err
		}
		mode := engine.Lenient
		if cfg.BSSCI.Mode == "strict" {
			mode = engine.Strict
		}
		bs, err = bssci.New(bssci.Config{
			SCEui: env.EUI, Schemas: env.Schemas, Registry: reg, Log: log.With("iface", "bssci"), Trace: sink, TLS: tlsCfg,
			Policy: policy, Mode: mode, Unknown: cfg.BSSCI.UnknownBaseStations, ResponseTimeout: cfg.BSSCI.ResponseTimeout,
			IdlePing: cfg.BSSCI.IdlePing, StatusInterval: cfg.BSSCI.StatusInterval, DedupWindow: cfg.BSSCI.DedupWindow,
			Name: "simple-sc", SwVersion: buildinfo.Version,
		})
		if err != nil {
			return err
		}
		ln, err := net.Listen("tcp", cfg.BSSCI.Listen)
		if err != nil {
			return fmt.Errorf("BSSCI listen: %w", err)
		}
		log.Info("BSSCI listening", "addr", cfg.BSSCI.Listen, "versions", env.Schemas.Versions(bssci.Interface), "unknown_base_stations", cfg.BSSCI.UnknownBaseStations)
		go func() { errc <- bs.Serve(ctx, tls.NewListener(ln, tlsCfg)) }()
	}
	if cfg.Admin.Listen != "" && cfg.Admin.Listen != "-" {
		srv := &http.Server{Addr: cfg.Admin.Listen, Handler: (&api.API{Reg: reg, BSSCI: bs, SCEui: env.EUI}).Handler(), ReadHeaderTimeout: 5 * time.Second}
		go func() { <-ctx.Done(); _ = srv.Close() }()
		go func() {
			log.Info("admin REST listening", "addr", cfg.Admin.Listen)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}
	log.Info("started", "version", buildinfo.Version, "scaci", "follows in M2")
	select {
	case <-ctx.Done():
	case err := <-errc:
		if err != nil {
			return err
		}
	}
	log.Info("stopped")
	return nil
}

// serverTLS uses the configured files, or the lab CA and server certificate.
func serverTLS(o config.BSSCIOptions, caDir string) (*tls.Config, error) {
	cert, key, ca := o.CertFile, o.KeyFile, o.CAFile
	if cert == "" {
		cert, key = filepath.Join(caDir, "sc.pem"), filepath.Join(caDir, "sc.key")
	}
	if ca == "" {
		ca = filepath.Join(caDir, labca.CACert)
	}
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return nil, fmt.Errorf("server certificate: %w", err)
	}
	caPEM, err := os.ReadFile(ca)
	if err != nil {
		return nil, fmt.Errorf("CA for base stations: %w", err)
	}
	pool, err := bssci.ClientCAPool(caPEM)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}, nil
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
