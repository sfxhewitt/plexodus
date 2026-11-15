package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/db"
)

// resetPassword implements `plexodus reset-password`: it sets a new password
// for the admin account (or -user NAME) straight in the database, for someone
// who has forgotten it. Anyone who can run it can already read and write the
// data folder, so it needs no other proof. The account is signed out
// everywhere. It works while the server is running.
func resetPassword(args []string) error {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	data := fs.String("data", defaultDataDir(), "data directory (the one the server runs with)")
	user := fs.String("user", "", "account to reset (default: the admin account)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: plexodus reset-password [-data DIR] [-user NAME]")
		fmt.Fprintln(fs.Output(), "Sets a new password, typed in or piped on stdin, for the admin account or -user NAME.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := filepath.Join(*data, "plexodus.db")
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("no Plexodus database at %s (use -data to point at the data folder): %w", path, err)
	}
	// Run as the database's owner when started with sudo, so any file
	// SQLite creates (the -wal and -shm files) stays readable by the server.
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && os.Geteuid() == 0 && sys.Uid != 0 {
		if err := syscall.Setgid(int(sys.Gid)); err != nil {
			return fmt.Errorf("switch group: %w", err)
		}
		if err := syscall.Setuid(int(sys.Uid)); err != nil {
			return fmt.Errorf("switch user: %w", err)
		}
	}

	ctx := context.Background()
	d, err := db.Open(path)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer d.Close()

	var id int64
	var name string
	if *user != "" {
		err = d.R.QueryRowContext(ctx, `SELECT id, username FROM users WHERE username = ? AND parent_id IS NULL`,
			strings.TrimSpace(*user)).Scan(&id, &name)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("there's no account called %q. Accounts: %s", *user, accountNames(ctx, d))
		}
	} else {
		err = d.R.QueryRowContext(ctx, `SELECT id, username FROM users WHERE is_admin = 1 AND parent_id IS NULL ORDER BY id LIMIT 1`).Scan(&id, &name)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("there's no admin account yet: open Plexodus in a browser to create one")
		}
	}
	if err != nil {
		return err
	}

	pass, err := readNewPassword(name)
	if err != nil {
		return err
	}
	a := auth.New(d)
	if err := a.SetPassword(ctx, id, pass); err != nil {
		return err
	}
	if _, err := d.W.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return fmt.Errorf("sign out old sessions: %w", err)
	}
	fmt.Printf("Password changed for %q.\n", name)
	return nil
}

func defaultDataDir() string {
	if v := os.Getenv("PLEXODUS_DATA"); v != "" {
		return v
	}
	if _, err := os.Stat("/var/lib/plexodus/plexodus.db"); err == nil {
		return "/var/lib/plexodus"
	}
	return "./data"
}

func accountNames(ctx context.Context, d *db.DB) string {
	rows, err := d.R.QueryContext(ctx, `SELECT username FROM users WHERE parent_id IS NULL ORDER BY id`)
	if err != nil {
		return "?"
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	return strings.Join(names, ", ")
}

// readNewPassword asks twice, without echo, on a terminal; otherwise it
// reads the first line of stdin.
func readNewPassword(name string) (string, error) {
	if !isTerminal(os.Stdin) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && !(errors.Is(err, io.EOF) && line != "") {
			return "", errors.New("no password given on stdin")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	in := bufio.NewReader(os.Stdin)
	ask := func(prompt string) (string, error) {
		fmt.Print(prompt)
		echo(false)
		defer func() { echo(true); fmt.Println() }()
		line, err := in.ReadString('\n')
		if err != nil {
			return "", errors.New("cancelled")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	for {
		p1, err := ask(fmt.Sprintf("New password for %q (nothing shows while you type): ", name))
		if err != nil {
			return "", err
		}
		if len(p1) < 6 {
			fmt.Println("Please use at least 6 characters.")
			continue
		}
		p2, err := ask("Type it again: ")
		if err != nil {
			return "", err
		}
		if p1 != p2 {
			fmt.Println("Those didn't match. Let's try again.")
			continue
		}
		return p1, nil
	}
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func echo(on bool) {
	arg := "-echo"
	if on {
		arg = "echo"
	}
	cmd := exec.Command("stty", arg)
	cmd.Stdin = os.Stdin
	cmd.Run()
}
