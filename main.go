package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"go-web-side/installer"
	"go-web-side/web"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "worker" {
		if err := worker(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "cli" {
		if err := cli(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	port := flag.Int("port", 0, "port to listen on (0 picks a free port)")
	noBrowser := flag.Bool("no-browser", false, "don't open the default browser")
	keepAlive := flag.Bool("keep-alive", false, "keep running after the browser window is closed")
	flag.Parse()

	if err := run(*port, !*noBrowser, *keepAlive); err != nil {
		log.Fatal(err)
	}
}

// worker runs `worker <install|uninstall> -scope <user|machine> [-path dir] [-result file]`
// without the web UI. The installer re-runs itself this way when elevated.
func worker(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: worker <install|uninstall> -scope <user|machine> [-path dir] [-result file]")
	}
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	scopeFlag := fs.String("scope", "user", "user or machine")
	path := fs.String("path", "", "install folder (default: the scope's standard location)")
	result := fs.String("result", "", "file to write the JSON result to")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	scope, err := installer.ParseScope(*scopeFlag)
	if err != nil {
		return err
	}
	return installer.RunWorker(args[0], scope, *path, *result)
}

func run(port int, openBrowser, keepAlive bool) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	// Exit when the window is closed, but only if we opened one.
	windowClosed := make(chan struct{}, 1)
	var onIdle func()
	if openBrowser && !keepAlive {
		onIdle = func() {
			select {
			case windowClosed <- struct{}{}:
			default:
			}
		}
	}
	handler, err := web.Handler(ln.Addr().String(), onIdle)
	if err != nil {
		return err
	}
	url := "http://" + ln.Addr().String()

	// Cancelling reqCtx ends long-lived requests so Shutdown does not wait on them.
	reqCtx, cancelReqs := context.WithCancel(context.Background())
	defer cancelReqs()
	srv := &http.Server{Handler: handler, BaseContext: func(net.Listener) context.Context { return reqCtx }}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	fmt.Println("Serving on", url, "(Ctrl+C to stop)")
	if openBrowser {
		if err := open(url); err != nil {
			fmt.Fprintln(os.Stderr, "could not open browser:", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	select {
	case err := <-errc:
		return err
	case <-windowClosed:
		fmt.Println("Window closed; shutting down.")
	case <-ctx.Done():
	}

	cancelReqs()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
