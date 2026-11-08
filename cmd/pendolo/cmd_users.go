package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/auth"
	"github.com/enthonysclafani/automatizzatore/internal/store"
	"github.com/enthonysclafani/automatizzatore/internal/vault"
)

// stdin is replaced in tests.
var stdin io.Reader = os.Stdin

func cmdUsers(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: pendolo users list | create --email e [--name n] [--owner] [--workspace w --role r] | password --email e")
		return exitUsage
	}
	switch args[0] {
	case "list":
		return usersList(args[1:], stdout, stderr)
	case "create":
		return usersCreate(args[1:], stdout, stderr)
	case "password":
		return usersPassword(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown users command %q\n", args[0])
		return exitUsage
	}
}

// readPassword reads a password line from stdin (pipe it, or type it).
func readPassword(stderr io.Writer) (string, error) {
	fmt.Fprintf(stderr, "password (at least %d characters): ", auth.MinPasswordLength)
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	fmt.Fprintln(stderr)
	return strings.TrimRight(line, "\r\n"), nil
}

func usersList(args []string, stdout, stderr io.Writer) int {
	c := newCommand("users list", stderr, true)
	if _, err := c.parse(args); err != nil {
		return exitUsage
	}
	st, _, err := c.openStore()
	if err != nil {
		return fail(stderr, err)
	}
	defer st.Close()
	users, err := st.ListUsers(context.Background())
	if err != nil {
		return fail(stderr, err)
	}
	if c.json() {
		printJSON(stdout, users)
		return exitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "EMAIL\tNAME\tOWNER\t2FA\tDISABLED\tLAST LOGIN")
	for _, u := range users {
		fmt.Fprintf(tw, "%s\t%s\t%v\t%v\t%v\t%s\n", u.Email, u.Name, u.IsOwner, u.TOTPEnabled, u.Disabled, local(u.LastLoginAt))
	}
	_ = tw.Flush()
	return exitOK
}

func usersCreate(args []string, stdout, stderr io.Writer) int {
	c := newCommand("users create", stderr, false)
	email := c.fs.String("email", "", "email address (required)")
	name := c.fs.String("name", "", "display name")
	owner := c.fs.Bool("owner", false, "make the user an instance owner")
	workspace := c.fs.String("workspace", "", "add the user to this workspace")
	role := c.fs.String("role", "viewer", "role in --workspace: viewer, approver, builder or admin")
	if _, err := c.parse(args); err != nil {
		return exitUsage
	}
	if *email == "" {
		fmt.Fprintln(stderr, "--email is required")
		return exitUsage
	}
	if *workspace != "" && !store.ValidRole(*role) {
		fmt.Fprintln(stderr, "--role must be viewer, approver, builder or admin")
		return exitUsage
	}
	pw, err := readPassword(stderr)
	if err != nil {
		return fail(stderr, err)
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return fail(stderr, err)
	}
	st, _, err := c.openStore()
	if err != nil {
		return fail(stderr, err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Now()
	u := &store.User{Email: *email, Name: *name, PasswordHash: hash, IsOwner: *owner, CreatedAt: now}
	if err := st.CreateUser(ctx, u); errors.Is(err, store.ErrConflict) {
		return fail(stderr, fmt.Errorf("a user with email %s exists; use `pendolo users password` to reset it", *email))
	} else if err != nil {
		return fail(stderr, err)
	}
	if *workspace != "" {
		if err := st.EnsureWorkspace(ctx, *workspace, now); err != nil {
			return fail(stderr, err)
		}
		if err := st.SetMembership(ctx, u.ID, *workspace, *role); err != nil {
			return fail(stderr, err)
		}
	}
	_ = st.Audit(ctx, store.AuditEntry{At: now, Actor: "cli", Action: "user.create", Target: u.Email, Detail: map[string]any{"owner": *owner}})
	fmt.Fprintf(stdout, "created %s (owner: %v)\n", u.Email, u.IsOwner)
	return exitOK
}

func usersPassword(args []string, stdout, stderr io.Writer) int {
	c := newCommand("users password", stderr, false)
	email := c.fs.String("email", "", "email address (required)")
	if _, err := c.parse(args); err != nil {
		return exitUsage
	}
	if *email == "" {
		fmt.Fprintln(stderr, "--email is required")
		return exitUsage
	}
	st, _, err := c.openStore()
	if err != nil {
		return fail(stderr, err)
	}
	defer st.Close()
	ctx := context.Background()
	u, err := st.GetUserByEmail(ctx, *email)
	if err != nil {
		return fail(stderr, fmt.Errorf("no user %s", *email))
	}
	pw, err := readPassword(stderr)
	if err != nil {
		return fail(stderr, err)
	}
	if u.PasswordHash, err = auth.HashPassword(pw); err != nil {
		return fail(stderr, err)
	}
	// A reset by an operator also clears two-factor, for a locked-out user.
	u.TOTPEnabled, u.TOTPSecret, u.TOTPLast, u.Disabled = false, "", 0, false
	if err := st.UpdateUser(ctx, u); err != nil {
		return fail(stderr, err)
	}
	_ = st.DeleteUserSessions(ctx, u.ID, "")
	_ = st.Audit(ctx, store.AuditEntry{At: time.Now(), Actor: "cli", Action: "user.password_reset", Target: u.Email})
	fmt.Fprintf(stdout, "password reset for %s; two-factor authentication turned off\n", u.Email)
	return exitOK
}

func cmdKeygen(_ []string, stdout, stderr io.Writer) int {
	key, err := vault.NewKey()
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, key)
	return exitOK
}
