// Command trace-view prints the message trace of simple-sc or simple-ac in a readable form.
//
//	trace-view data/trace/trace.jsonl
//	trace-view -conn bssci/70-B3-D5-9C-D0-00-09-E2 -cmd att -full data/trace/trace.jsonl
//	trace-view -f data/trace/trace.jsonl      follow the file
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

type record struct {
	Time     time.Time      `json:"time"`
	Conn     string         `json:"conn"`
	Version  string         `json:"version"`
	Dir      string         `json:"dir"`
	Command  string         `json:"command"`
	OpID     any            `json:"opId"`
	Message  map[string]any `json:"msg"`
	Findings []string       `json:"findings"`
	Note     string         `json:"note"`
}

func main() {
	conn := flag.String("conn", "", "only this connection (substring)")
	cmd := flag.String("cmd", "", "only commands starting with this, e.g. att")
	full := flag.Bool("full", false, "print every field")
	follow := flag.Bool("f", false, "follow the file")
	flag.Parse()
	var in io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		in = f
	}
	r := bufio.NewReader(in)
	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			print(line, *conn, *cmd, *full)
		}
		if err == io.EOF {
			if !*follow {
				return
			}
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
	}
}

func print(line, conn, cmd string, full bool) {
	var rec record
	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber() // keep 64 bit values exact
	if err := dec.Decode(&rec); err != nil {
		return
	}
	if conn != "" && !strings.Contains(rec.Conn, conn) {
		return
	}
	if cmd != "" && !strings.HasPrefix(rec.Command, cmd) {
		return
	}
	arrow := "->"
	if rec.Dir == "in" {
		arrow = "<-"
	}
	head := fmt.Sprintf("%s %-36s %s %-12s %v", rec.Time.Local().Format("15:04:05.000"), rec.Conn, arrow, rec.Command, rec.OpID)
	if rec.Note != "" {
		fmt.Printf("%s  NOTE %s\n", head, rec.Note)
		return
	}
	keys := make([]string, 0, len(rec.Message))
	for k := range rec.Message {
		if k != "command" && k != "opId" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := fmt.Sprint(rec.Message[k])
		if !full && len(v) > 40 {
			v = v[:37] + "..."
		}
		parts = append(parts, k+"="+v)
	}
	if !full && len(parts) > 8 {
		parts = append(parts[:8], fmt.Sprintf("(+%d)", len(parts)-8))
	}
	fmt.Printf("%s  %s\n", head, strings.Join(parts, " "))
	for _, f := range rec.Findings {
		fmt.Printf("    finding: %s\n", f)
	}
}
