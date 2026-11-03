// whatsapp-sidecar: watches the WhatsApp MCP server's message database and publishes every new incoming message to MQTT.
//
// Why this exists: the WhatsApp server (Sealjay/mcp-whatsapp) stores incoming messages in a SQLite file but has no way
// to push them anywhere. This program runs as a second container in the same pod, shares the volume, checks the file
// about once a second and turns each new row into an MQTT message. See ../README.md ("acting on incoming messages").
//
// Reading guide for someone new to Go: every Go file starts with a package name. A program that can be run is always
// `package main`, and execution starts at `func main()`. Comments that start with "GO:" explain a language feature the
// first time it appears.
package main

// GO: `import` lists the packages (libraries) used below. Names without dots, such as "fmt", are the standard library
// that ships with Go. Names that look like URLs are third-party modules, downloaded when the program is built.
// Go refuses to compile a file that imports something it never uses, so this list is always exactly what is needed.
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	// GO: `mqtt "..."` gives the package a short local name. This is the MQTT client library.
	mqtt "github.com/eclipse/paho.mqtt.golang"

	// GO: a leading underscore imports a package only for its side effect. This pure-Go SQLite driver registers itself
	// under the name "sqlite" when imported, and database/sql then finds it by that name in sql.Open below. "Pure Go"
	// means no C compiler is needed, which keeps the container build simple.
	_ "modernc.org/sqlite"
)

// config holds the settings read from environment variables.
//
// GO: a `struct` groups named fields into one value, a bit like a class with only data. Field names that start with a
// lower-case letter are private to this package, which is fine here because everything is in one package.
type config struct {
	dbPath       string        // path of messages.db on the shared volume
	mqttURL      string        // broker address, for example tcp://mqtt.automation.svc.cluster.local:1883
	topicPrefix  string        // every message is published to <topicPrefix>/<chat jid>
	pollInterval time.Duration // how often to look for new rows
	maxAge       time.Duration // ignore messages older than this (guards against history-sync backfills)
}

// message is what gets published, as JSON.
//
// GO: the text in backticks after each field is a "struct tag". The encoding/json package reads it to choose the JSON
// key, so the Go field `ChatJID` becomes "chat_jid" on the wire. Field names must start with a capital letter for the
// json package to see them (capital means exported, lower-case means private).
type message struct {
	ID        string `json:"id"`
	ChatJID   string `json:"chat_jid"`
	Sender    string `json:"sender"`
	Content   string `json:"content"`
	MediaType string `json:"media_type"`
	Timestamp int64  `json:"timestamp"` // Unix seconds, UTC
	IsGroup   bool   `json:"is_group"`
}

// row pairs a message with its SQLite rowid. The rowid is not published; it is only used to remember how far we have
// read, because it always increases when a row is inserted.
type row struct {
	rowid int64
	msg   message
}

// newMessagesQuery selects incoming messages (is_from_me = 0, so the account's own sends never trigger anything)
// that are newer than the rowid given as the single `?` placeholder.
//
// GO: a string in backticks is a "raw string": it can span several lines and backslashes are not special.
// Details of the SQL: timestamp is stored as text such as "2026-11-03 19:50:20+00:00". SQLite's strftime('%s', ...)
// turns it into Unix seconds. If the text cannot be parsed, strftime returns NULL and the Scan below fails loudly,
// instead of this program guessing. COALESCE replaces a NULL in columns that may legitimately be empty (for example
// the text of a photo) with an empty string.
const newMessagesQuery = `
SELECT rowid, id, chat_jid, COALESCE(sender, ''), COALESCE(content, ''), COALESCE(media_type, ''),
       CAST(strftime('%s', timestamp) AS INTEGER)
FROM messages
WHERE rowid > ? AND is_from_me = 0
ORDER BY rowid`

// mustEnv returns the value of an environment variable and an error if it is not set.
//
// GO: functions can return more than one value, and by convention the last one is an `error`. A nil error means
// success. There are no exceptions in Go: the caller checks the error after every call that can fail (you will see
// `if err != nil` all over this file).
func mustEnv(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("environment variable %s is required", key)
	}
	return v, nil
}

// envDuration reads an environment variable such as "1s" or "10m" as a time.Duration.
func envDuration(key string) (time.Duration, error) {
	v, err := mustEnv(key)
	if err != nil {
		return 0, err
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		// GO: `%w` wraps the original error inside the new one so the cause is kept in the message.
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

// loadConfig reads every setting and stops at the first one that is missing or invalid. There are deliberately no
// defaults: the Deployment spells every value out, so a wrong or missing one is an error at start-up.
func loadConfig() (config, error) {
	var c config // GO: `var` declares a variable; it starts at the zero value (empty string, 0, ...).
	var err error

	// GO: `if a, err = f(); err != nil {...}` runs the statement before the `;` and then tests the condition.
	if c.dbPath, err = mustEnv("DB_PATH"); err != nil {
		return c, err
	}
	if c.mqttURL, err = mustEnv("MQTT_URL"); err != nil {
		return c, err
	}
	if c.topicPrefix, err = mustEnv("TOPIC_PREFIX"); err != nil {
		return c, err
	}
	if c.pollInterval, err = envDuration("POLL_INTERVAL"); err != nil {
		return c, err
	}
	if c.maxAge, err = envDuration("MAX_AGE"); err != nil {
		return c, err
	}
	return c, nil
}

// maxRowID returns the highest rowid currently in the messages table, or 0 if the table is empty. It is used once at
// start-up so that only messages arriving after that point are published.
func maxRowID(ctx context.Context, db *sql.DB) (int64, error) {
	var n int64
	// GO: Scan copies the columns of the single result row into the variables passed by address. `&n` means "the
	// address of n", so Scan can write into it.
	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(rowid), 0) FROM messages`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("read highest rowid: %w", err)
	}
	return n, nil
}

// fetchNew returns every incoming message with a rowid greater than `after`, oldest first.
func fetchNew(ctx context.Context, db *sql.DB, after int64) ([]row, error) {
	rows, err := db.QueryContext(ctx, newMessagesQuery, after)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	// GO: `defer` runs a call when the surrounding function returns, however it returns. It is how Go makes sure
	// resources such as open result sets are always released.
	defer rows.Close()

	var out []row // GO: a slice is a growable list; `var` gives an empty one.
	for rows.Next() {
		var r row
		err := rows.Scan(&r.rowid, &r.msg.ID, &r.msg.ChatJID, &r.msg.Sender, &r.msg.Content, &r.msg.MediaType,
			&r.msg.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		// Group chats have JIDs ending in @g.us; one-to-one chats end in @s.whatsapp.net.
		r.msg.IsGroup = strings.HasSuffix(r.msg.ChatJID, "@g.us")
		out = append(out, r) // GO: append returns the longer slice, so the result is assigned back.
	}
	// rows.Next() also returns false when something went wrong part-way, so always check rows.Err() afterwards.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read messages: %w", err)
	}
	return out, nil
}

// publish sends one MQTT message and waits until the broker has acknowledged it.
func publish(client mqtt.Client, topic string, payload []byte) error {
	// QoS 1 means "at least once": the library resends until the broker confirms. retain=false means the broker does
	// not keep the message for subscribers that connect later.
	token := client.Publish(topic, 1, false, payload)
	// GO: the paho library is asynchronous. Publish returns a "token" straight away and the work happens in the
	// background; WaitTimeout blocks until it finishes (true) or the time runs out (false).
	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("publish to %s timed out", topic)
	}
	return token.Error()
}

// poll does one check of the database and publishes whatever is new. It returns the rowid to resume from next time.
//
// Failure handling: if the database read or a publish fails, the position is NOT advanced past the message that
// failed, the error is logged, and the next tick tries again. Nothing is silently dropped, and the error shows in
// `kubectl logs` every tick until it is fixed. (A restart forgets the position, see the README for that limit.)
func poll(ctx context.Context, db *sql.DB, client mqtt.Client, cfg config, last int64) int64 {
	rows, err := fetchNew(ctx, db, last)
	if err != nil {
		log.Printf("ERROR: %v", err)
		return last
	}

	// GO: `for _, r := range rows` loops over a slice. The first value is the index, and `_` throws it away because
	// it is not needed.
	for _, r := range rows {
		// WhatsApp's history sync (for example after a re-pair) inserts old messages with new rowids. Those are not
		// "new", so anything older than maxAge is skipped, but the position still moves past it.
		age := time.Since(time.Unix(r.msg.Timestamp, 0))
		if age > cfg.maxAge {
			log.Printf("skipped old message (age %s, rowid %d)", age.Round(time.Second), r.rowid)
			last = r.rowid
			continue
		}

		// GO: json.Marshal turns a struct into JSON bytes, using the struct tags defined above.
		payload, err := json.Marshal(r.msg)
		if err != nil {
			// Cannot happen for this struct unless the code is wrong, so stop loudly. log.Fatalf prints and exits.
			log.Fatalf("encode message: %v", err)
		}

		topic := cfg.topicPrefix + "/" + r.msg.ChatJID
		if err := publish(client, topic, payload); err != nil {
			log.Printf("ERROR: %v (will retry on the next tick, rowid %d)", err, r.rowid)
			return last
		}
		// The message text is not logged on purpose: logs are less private than the chat.
		log.Printf("published rowid %d to %s", r.rowid, topic)
		last = r.rowid
	}
	return last
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	// Kubernetes stops a pod by sending SIGTERM. NotifyContext returns a `ctx` that is cancelled when that signal (or
	// Ctrl-C) arrives, which lets the loop below exit cleanly. GO: a Context carries a cancellation signal through
	// the calls that accept one, including the database queries.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Open the database read-only. mode=ro makes SQLite refuse every write, and query_only is a second guard. The WAL
	// files next to the database still need to be writable by this process (the volume is mounted read-write), because
	// even a reader has to update the shared-memory index file. busy_timeout(5000) waits up to 5 s if the server holds
	// a lock instead of failing at once.
	// GO: sql.Open does not connect yet; PingContext below forces a real connection so a bad path fails right here.
	dsn := "file:" + cfg.dbPath + "?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("connect to database %s: %v", cfg.dbPath, err)
	}

	// Build the MQTT client. AutoReconnect makes the library reconnect by itself if the broker restarts.
	// GO: the dots chain method calls; each Set... returns the options object so the calls can be strung together.
	opts := mqtt.NewClientOptions().
		AddBroker(cfg.mqttURL).
		SetClientID("whatsapp-sidecar").
		SetAutoReconnect(true).
		SetConnectionLostHandler(func(_ mqtt.Client, err error) {
			// GO: `func(...) {...}` with no name is an anonymous function, passed here as a callback.
			log.Printf("mqtt connection lost: %v", err)
		})
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalf("connect to mqtt %s: %v", cfg.mqttURL, token.Error())
	}
	defer client.Disconnect(250) // wait up to 250 ms for in-flight work, then close

	// Start from the newest row that already exists, so a restart does not re-publish old messages.
	last, err := maxRowID(ctx, db)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("watching %s from rowid %d, publishing to %s/<chat jid> on %s", cfg.dbPath, last, cfg.topicPrefix,
		cfg.mqttURL)

	// The main loop: wake up every pollInterval and check for new rows, until the pod is told to stop.
	ticker := time.NewTicker(cfg.pollInterval)
	defer ticker.Stop()
	for {
		// GO: `select` waits on several channels at once and runs the case for whichever is ready first. A channel
		// is Go's way of passing values between concurrent parts of a program. Here: ctx.Done() fires on shutdown,
		// ticker.C fires once per interval.
		select {
		case <-ctx.Done():
			log.Print("shutting down")
			return
		case <-ticker.C:
			last = poll(ctx, db, client, cfg, last)
		}
	}
}
