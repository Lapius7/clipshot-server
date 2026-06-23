// Package cli implements the `clipshot-server token ...` admin subcommands.
package cli

import (
	"database/sql"
	"flag"
	"fmt"
	"os"

	"github.com/Lapius7/clipshot-server/internal/auth"
)

// Run dispatches admin subcommands. It returns true if a subcommand was
// handled (and the caller should exit), false if the caller should fall
// through to starting the HTTP server.
func Run(args []string, openDB func() (*sql.DB, error)) bool {
	if len(args) < 1 || args[0] != "token" {
		return false
	}
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: clipshot-server token <create|revoke> ...")
		os.Exit(1)
	}

	db, err := openDB()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error opening database:", err)
		os.Exit(1)
	}
	defer db.Close()

	switch args[1] {
	case "create":
		fs := flag.NewFlagSet("token create", flag.ExitOnError)
		label := fs.String("label", "default", "human-readable label for this token")
		fs.Parse(args[2:])

		plaintext, err := auth.CreateToken(db, *label)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error creating token:", err)
			os.Exit(1)
		}
		fmt.Println("Token created. Save this value now -- it will not be shown again:")
		fmt.Println(plaintext)

	case "revoke":
		fs := flag.NewFlagSet("token revoke", flag.ExitOnError)
		id := fs.String("id", "", "token id to revoke")
		fs.Parse(args[2:])
		if *id == "" {
			fmt.Fprintln(os.Stderr, "error: -id is required")
			os.Exit(1)
		}
		if err := auth.RevokeToken(db, *id); err != nil {
			fmt.Fprintln(os.Stderr, "error revoking token:", err)
			os.Exit(1)
		}
		fmt.Println("Token revoked:", *id)

	default:
		fmt.Fprintln(os.Stderr, "usage: clipshot-server token <create|revoke> ...")
		os.Exit(1)
	}

	return true
}
