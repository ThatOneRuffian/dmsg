package dmsgget

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/disc"
	"github.com/skycoin/dmsg/dmsghttp"
)

// DmsgGet contains the logic for dmsgget (wget over dmsg).
type DmsgGet struct {
	startF startupFlags
	dmsgF  dmsgFlags
	dlF    downloadFlags
	httpF  httpFlags
	fs     *flag.FlagSet
}

// New creates a new DmsgGet instance.
func New(fs *flag.FlagSet) *DmsgGet {
	dg := &DmsgGet{fs: fs}

	for _, fg := range dg.flagGroups() {
		fg.Init(fs)
	}

	w := fs.Output()
	flag.Usage = func() {
		_, _ = fmt.Fprintf(w, "Skycoin %s %s, wget over dmsg.\n", ExecName, Version)
		_, _ = fmt.Fprintf(w, "Usage: %s [OPTION]... [URL]\n\n", ExecName)
		flag.PrintDefaults()
		_, _ = fmt.Fprintln(w, "")
	}

	return dg
}

// String implements io.Stringer
func (dg *DmsgGet) String() string {
	m := make(map[string]interface{})
	for _, fg := range dg.flagGroups() {
		m[fg.Name()] = fg
	}
	j, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(j)
}

func (dg *DmsgGet) flagGroups() []FlagGroup {
	return []FlagGroup{&dg.startF, &dg.dlF, &dg.httpF}
}

// Run runs the download logic.
func (dg *DmsgGet) Run(ctx context.Context, log logrus.FieldLogger, skStr string, args []string) (err error) {

	if dg.startF.Help {
		dg.fs.Usage()
		return nil
	}

	pk, sk, err := parseKeyPair(skStr)
	if err != nil {
		return fmt.Errorf("failed to parse provided key pair: %w", err)
	}

	u, err := parseURL(args)
	if err != nil {
		return fmt.Errorf("failed to parse provided URL: %w", err)
	}

	file, err := parseOutputFile(dg.dlF.Output, u.URL.Path)
	if err != nil {
		return fmt.Errorf("failed to prepare output file: %w", err)
	}
	defer func() {
		if fErr := file.Close(); fErr != nil {
			fmt.Println("Failed to close output file.")
		}
		if err != nil {
			if rErr := os.RemoveAll(file.Name()); rErr != nil {
				fmt.Println("Failed to remove output file.")
				panic(1)
			}
		}
	}()

	dmsgC, closeDmsg, err := dg.startDmsg(ctx, pk, sk)
	if err != nil {
		return fmt.Errorf("failed to start dmsg: %w", err)
	}
	defer closeDmsg()

	httpC := http.Client{Transport: dmsghttp.MakeHTTPTransport(dmsgC)}

	for i := 0; i < dg.dlF.Tries; i++ {
		fmt.Println("Download attempt %d/%d ...", i, dg.dlF.Tries)

		if _, err := file.Seek(0, 0); err != nil {
			return fmt.Errorf("failed to reset file: %w", err)
		}

		if err := Download(&httpC, file, u.URL.String()); err != nil {
			fmt.Println(err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(dg.dlF.Wait) * time.Second):
				continue
			}
		}

		// download successful.
		return nil
	}

	return errors.New("all download attempts failed")
}

func parseKeyPair(skStr string) (pk cipher.PubKey, sk cipher.SecKey, err error) {
	if skStr == "" {
		pk, sk = cipher.GenerateKeyPair()
		return
	}

	if err = sk.Set(skStr); err != nil {
		return
	}

	pk, err = sk.PubKey()
	return
}

func parseURL(args []string) (*URL, error) {
	if len(args) == 0 {
		return nil, ErrNoURLs
	}

	if len(args) > 1 {
		return nil, ErrMultipleURLsNotSupported
	}

	var out URL
	if err := out.Fill(args[0]); err != nil {
		return nil, fmt.Errorf("provided URL is invalid: %w", err)
	}

	return &out, nil
}

func parseOutputFile(name string, urlPath string) (*os.File, error) {
	stat, statErr := os.Stat(name)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			f, err := os.Create(name)
			if err != nil {
				return nil, err
			}
			return f, nil
		}
		return nil, statErr
	}

	if stat.IsDir() {
		f, err := os.Create(filepath.Join(name, urlPath))
		if err != nil {
			return nil, err
		}
		return f, nil
	}

	return nil, os.ErrExist
}
func (dg *DmsgGet) startDmsg(ctx context.Context, log logrus.FieldLogger, pk cipher.PubKey, sk cipher.SecKey) (dmsgC *dmsg.Client, stop func(), err error) {

	dmsgC = dmsg.NewClient(pk, sk, disc.NewHTTP(dg.dmsgF.Disc), &dmsg.Config{MinSessions: dg.dmsgF.Sessions})
	go dmsgC.Serve(context.Background())

	stop = func() {
		if err := dmsgC.Close(); err != nil {
			fmt.Println(err)
		}

		fmt.Println("Disconnected from dmsg network.")
	}
	log.WithField("public_key", pk.String()).WithField("dmsg_disc", dg.dmsgF.Disc).
		Info("Connecting to dmsg network...")

	select {
	case <-ctx.Done():
		stop()
		return nil, nil, ctx.Err()

	case <-dmsgC.Ready():
		fmt.Println("Dmsg network ready.")
		return dmsgC, stop, nil
	}
}

// Download downloads a file from the given URL into 'w'.
func Download(httpC *http.Client, w io.Writer, urlStr string) error {
	req, err := http.NewRequest(http.MethodGet, urlStr, nil)
	if err != nil {
		fmt.Println("Failed to formulate HTTP request.")
		panic(1)
	}

	resp, err := httpC.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to HTTP server: %w", err)
		panic(1)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Println("HTTP Response body closed with non-nil error.")
			panic(1)
		}
	}()

	n, err := io.Copy(io.MultiWriter(w, &ProgressWriter{Total: resp.ContentLength}), resp.Body)
	if err != nil {
		return fmt.Errorf("download failed at %d/%dB: %w", n, resp.ContentLength, err)
	}
	return nil
}
