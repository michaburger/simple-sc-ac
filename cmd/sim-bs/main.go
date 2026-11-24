// Command sim-bs is a simulated base station. It connects to a Service Center over BSSCI,
// attaches simulated end-points and sends uplinks.
//
//	sim-bs -sc localhost:16107 -data ./data -ep 02-00-00-00-00-00-00-01:<key> -uplinks 3
//
// -data points to the data directory of a local simple-sc: the tool then takes the lab CA
// from there and issues itself a certificate. The keys of simulated end-points are test
// keys you generate, never keys of real devices.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/michaburger/simple-sc-ac/internal/common/app"
	"github.com/michaburger/simple-sc-ac/internal/common/idt"
	"github.com/michaburger/simple-sc-ac/internal/common/labca"
	"github.com/michaburger/simple-sc-ac/internal/sim/bs"
)

type epList []string

func (e *epList) String() string     { return strings.Join(*e, ",") }
func (e *epList) Set(v string) error { *e = append(*e, v); return nil }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sim-bs:", err)
		os.Exit(1)
	}
}

func run() error {
	scAddr := flag.String("sc", "localhost:16107", "address of the Service Center")
	euiS := flag.String("eui", "02-00-00-00-00-00-0B-01", "EUI-64 of the simulated base station")
	ver := flag.String("version", "1.1.0", "BSSCI version to request")
	dataDir := flag.String("data", "data", "data directory of a local simple-sc (lab CA)")
	schemaDir := flag.String("schemas", "", "schema directory, default <data>/schemas")
	serverName := flag.String("server-name", "", "TLS server name, default the host of -sc")
	uplinks := flag.Int("uplinks", 1, "uplinks per end-point")
	interval := flag.Duration("interval", time.Second, "time between uplinks")
	detach := flag.Bool("detach", false, "detach the end-points at the end")
	var eps epList
	flag.Var(&eps, "ep", "simulated end-point EUI:KEY (test key, 32 hex digits), repeatable")
	flag.Parse()

	eui, err := idt.ParseEUI64(*euiS)
	if err != nil {
		return err
	}
	if *schemaDir == "" {
		*schemaDir = filepath.Join(*dataDir, "schemas")
	}
	set, err := app.LoadSchemas(*schemaDir)
	if err != nil {
		return err
	}
	ca, _, err := labca.Init(filepath.Join(*dataDir, "ca"), "simple-sc lab CA")
	if err != nil {
		return err
	}
	certDir := filepath.Join(*dataDir, "ca", "issued")
	name := "sim-bs-" + eui.String()
	if _, err := os.Stat(filepath.Join(certDir, name+".pem")); os.IsNotExist(err) {
		if _, err := ca.Issue(certDir, labca.Request{Name: name, EUI: eui.String()}); err != nil {
			return err
		}
	}
	cert, err := tls.LoadX509KeyPair(filepath.Join(certDir, name+".pem"), filepath.Join(certDir, name+".key"))
	if err != nil {
		return err
	}
	sn := *serverName
	if sn == "" {
		sn = strings.Split(*scAddr, ":")[0]
	}
	ctx := context.Background()
	b, err := bs.Dial(ctx, *scAddr, bs.Config{EUI: eui, Version: *ver, Bidi: true, Schemas: set,
		TLS: &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: ca.Pool(), ServerName: sn}})
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer b.Close()
	fmt.Printf("connected as %s, session version %s\n", eui, b.Session)

	var sims []*bs.EndPoint
	for _, e := range eps {
		euiPart, keyPart, ok := strings.Cut(e, ":")
		if !ok {
			return fmt.Errorf("-ep %q: want EUI:KEY", e)
		}
		epEUI, err := idt.ParseEUI64(euiPart)
		if err != nil {
			return err
		}
		key, err := idt.ParseKey(keyPart)
		if err != nil {
			return err
		}
		sim := &bs.EndPoint{EUI: epEUI, NwkKey: key[:]}
		res, err := b.Attach(ctx, sim, 12, -75)
		if err != nil {
			fmt.Printf("attach %s: %v\n", epEUI, err)
			continue
		}
		fmt.Printf("attached %s, short address %s, session key matches: %v\n", epEUI, idt.ShortAddr(res.ShAddr), string(res.NwkSnKey) == string(res.Expected))
		sims = append(sims, sim)
	}
	for i := 1; i <= *uplinks; i++ {
		for _, sim := range sims {
			sim.PacketCnt++
			data := []byte{byte(i), 0xAB, 0xCD}
			if err := b.Uplink(ctx, sim, sim.PacketCnt, data, 12, -75); err != nil {
				fmt.Printf("uplink %s %d: %v\n", sim.EUI, sim.PacketCnt, err)
			} else {
				fmt.Printf("uplink %s packetCnt %d\n", sim.EUI, sim.PacketCnt)
			}
		}
		if i < *uplinks {
			time.Sleep(*interval)
		}
	}
	if *detach {
		for _, sim := range sims {
			if err := b.Detach(ctx, sim, 12, -75); err != nil {
				fmt.Printf("detach %s: %v\n", sim.EUI, err)
			} else {
				fmt.Printf("detached %s\n", sim.EUI)
			}
		}
	}
	return b.Disconnect(ctx)
}
