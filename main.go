package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type person struct {
	uuid           uuid.UUID
	name, nickname string
	birthday       time.Time
	stack          []string
}

type repo struct {
	conn *pgxpool.Pool
}

type dbWriter interface {
	run(ctx context.Context, p []*person) error
}

func main() {
	conn, err := pgx.Connect(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to database: %v\n", err)
		os.Exit(1)
	}

	defer conn.Close(context.Background())
}

func (r *repo) run(ctx context.Context, p []*person) error {
	batch := &pgx.Batch{}

	for _, person := range p {
		batch.Queue("INSERT INTO person(uuid, name, nickname, birthday) VALUES($1, $2, $3, $4)", person.uuid, person.name, person.nickname, person.birthday)
	}

	br := r.conn.SendBatch(ctx, batch)
	if err := br.Close(); err != nil {
		log.Fatal("could not release batch connection. err: ", err)
	}

	return nil
}

func writer(ctx context.Context, inC chan []*person, w dbWriter) {
	for {
		select {
		case <-ctx.Done():
			fmt.Println("writer done")
			return
		case batch := <-inC:
			w.run(ctx, batch)
		}
	}
}

// batcher takes a channel of people and returns a slice of pointers to people.
func batcher(ctx context.Context, inC chan *person, outC chan []*person, max int, d time.Duration) {
	var batch []*person
	timeout := time.After(d)

	reset := func() {
		batch = make([]*person, 0)
		timeout = time.After(d)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case p := <-inC:
			batch = append(batch, p)
			if len(batch) >= max {
				outC <- batch
				reset()
			}
		case <-timeout:
			fmt.Println("timeout ", len(batch))
			outC <- batch

			reset()
		}
	}
}
