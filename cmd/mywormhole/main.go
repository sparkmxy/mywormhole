package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"mywormhole/internal/wormhole"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		usage()
		return nil
	}
	command := os.Args[1]
	if command == "help" || command == "--help" || command == "-h" {
		usage()
		return nil
	}
	if command == "version" {
		fmt.Println("mywormhole 0.1.0 (protocol 1)")
		return nil
	}
	if command != "send" && command != "receive" && command != "resume" {
		return fmt.Errorf("unknown command %q; use mywormhole help", command)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	options := wormhole.Options{InviteToken: os.Getenv("MYWORMHOLE_INVITE_TOKEN")}
	flags.StringVar(&options.Server, "server", os.Getenv("MYWORMHOLE_SERVER"), "Cloudflare Worker origin (or MYWORMHOLE_SERVER)")
	flags.StringVar(&options.StatePath, "state", "", "explicit local resume-state path; contains session keys")
	flags.StringVar(&options.Output, "output", "", "receive destination filename; existing files are never overwritten")
	flags.IntVar(&options.Concurrency, "parallel", 4, "parallel chunks (1-8)")
	chunkMiB := flags.Int("chunk-mib", 16, "send chunk size in MiB (1-32)")
	flags.BoolVar(&options.Yes, "yes", false, "accept authenticated file offer without prompting")
	timeout := flags.Duration("timeout", 2*time.Hour, "maximum command duration")
	if err := flags.Parse(os.Args[2:]); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if *chunkMiB < 1 || *chunkMiB > 32 {
		return fmt.Errorf("chunk-mib must be 1-32")
	}
	if *timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	options.ChunkSize = int64(*chunkMiB) << 20
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	args := flags.Args()
	if command == "send" {
		if len(args) != 1 {
			return fmt.Errorf("usage: mywormhole send [flags] FILE (put flags before FILE)")
		}
		return wormhole.Send(ctx, args[0], options)
	}
	if command == "resume" {
		if len(args) != 1 {
			return fmt.Errorf("usage: mywormhole resume [flags] STATE_FILE")
		}
		return wormhole.Resume(ctx, args[0], options)
	}
	if len(args) > 1 {
		return fmt.Errorf("usage: mywormhole receive [flags] [CODE]")
	}
	code := ""
	if len(args) == 1 {
		code = args[0]
	} else {
		fmt.Fprint(os.Stderr, "Receive code: ")
		input := bufio.NewReader(os.Stdin)
		line, err := input.ReadString('\n')
		if err != nil {
			return err
		}
		code = strings.TrimSpace(line)
		options.Input = input
	}
	return wormhole.Receive(ctx, code, options)
}
func usage() {
	fmt.Print(`mywormhole — encrypted file transfer through your Cloudflare deployment

  mywormhole send [flags] FILE
  mywormhole receive [flags] [CODE]
  mywormhole resume [flags] STATE_FILE
  mywormhole version

Set MYWORMHOLE_SERVER on both computers.
Set MYWORMHOLE_INVITE_TOKEN on the sender only.
Use "mywormhole send -h" or "mywormhole receive -h" for flags.
Put flags before FILE, CODE or STATE_FILE.
Both computers must be online for initial SPAKE2 pairing.
`)
}
