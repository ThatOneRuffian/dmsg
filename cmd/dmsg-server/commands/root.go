package commands

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/spf13/cobra"

	"github.com/getsentry/sentry-go"
	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/buildinfo"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/cmdutil"
	"github.com/skycoin/dmsg/disc"
	"github.com/skycoin/dmsg/discord"
	"github.com/skycoin/dmsg/promutil"
	"github.com/skycoin/dmsg/servermetrics"
)

var (
	sf        cmdutil.ServiceFlags
	sentryDSN string
)

func init() {
	sf.Init(rootCmd, "dmsg_srv", "config.json")
	rootCmd.Flags().StringVarP(&sentryDSN, "sentry", "s", "", "address to send Sentry messages")

}

var rootCmd = &cobra.Command{
	Use:     "dmsg-server",
	Short:   "Dmsg Server for Skywire.",
	PreRunE: func(cmd *cobra.Command, args []string) error { return sf.Check() },
	Run: func(_ *cobra.Command, args []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		if discordWebhookURL := discord.GetWebhookURLFromEnv(); discordWebhookURL != "" {
			// Workaround for Discord logger hook. Actually, it's Info.
			fmt.Println(discord.StartLogMessage)
			defer fmt.Println(discord.StopLogMessage)
		} else {
			fmt.Println(discord.StartLogMessage)
			defer fmt.Println(discord.StopLogMessage)
		}

		var conf Config
		if err := sf.ParseConfig(os.Args, true, &conf); err != nil {
			fmt.Println(err.Error())
		}
		if sentryDSN != "" {
			err := sentry.Init(sentry.ClientOptions{
				Dsn:              sentryDSN,
				AttachStacktrace: true,
			})
			if err != nil {
				fmt.Println("sentry.Init: %s", err)
				os.Exit(1)
			}
			defer sentry.Flush(2 * time.Second)
		}

		m := prepareMetrics(sf.Tag, sf.MetricsAddr)

		lis, err := net.Listen("tcp", conf.LocalAddress)
		if err != nil {
			fmt.Printf("Error listening on %s: %v\n", conf.LocalAddress, err)
		}

		srvConf := dmsg.ServerConfig{
			MaxSessions:    conf.MaxSessions,
			UpdateInterval: conf.UpdateInterval,
		}
		srv := dmsg.NewServer(conf.PubKey, conf.SecKey, disc.NewHTTP(conf.Discovery), &srvConf, m)
		//srv.SetLogger(log)

		defer func() {
			fmt.Println("Closed server.")
			os.Exit(1)
		}()

		ctx, cancel := cmdutil.SignalContext(context.Background())
		defer cancel()

		go func() {
			if err := srv.Serve(lis, conf.PublicAddress); err != nil {
				fmt.Println("Serve: %v", err)
				cancel()
			}
		}()

		<-ctx.Done()
	},
}

// Config is a dmsg-server config
type Config struct {
	PubKey         cipher.PubKey `json:"public_key"`
	SecKey         cipher.SecKey `json:"secret_key"`
	Discovery      string        `json:"discovery"`
	LocalAddress   string        `json:"local_address"`
	PublicAddress  string        `json:"public_address"`
	MaxSessions    int           `json:"max_sessions"`
	UpdateInterval time.Duration `json:"update_interval"`
	LogLevel       string        `json:"log_level"`
}

func prepareMetrics(tag, addr string) servermetrics.Metrics {
	if addr == "" {
		return servermetrics.NewEmpty()
	}

	m := servermetrics.New(tag)

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	promutil.AddMetricsHandle(r, m.Collectors()...)

	fmt.Println("addr", addr)
	fmt.Println("Serving metrics...")
	go func() {
		fmt.Println(http.ListenAndServe(addr, r))
		os.Exit(1)
	}()

	return m
}

// Execute executes root CLI command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
}
