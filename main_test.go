package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBatcher(t *testing.T) {
	conn, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to database: %v\n", err)
		os.Exit(1)
	}

	dbWriter := &copyRepo{conn: conn}

	max := 50000
	batchCout := 5000
	ctx := context.Background()
	inC := make(chan *person, 100)
	outC := make(chan []*person, 100)

	go batcher(ctx, inC, outC, max, 30*time.Second)
	go batcher(ctx, inC, outC, max, 30*time.Second)

	go func() {
		for i := 0; i <= max*batchCout; i++ {
			inC <- &person{
				UUID:     uuid.New(),
				Name:     "test",
				Nickname: "test-nickame",
				Birthday: time.Now().Add(-30 * time.Hour * 24 * 365),
				Stack:    []string{"golang"},
			}
		}
	}()

	go func() {
		for i := 0; i <= max*batchCout; i++ {
			inC <- &person{
				UUID:     uuid.New(),
				Name:     "test",
				Nickname: "test-nickame",
				Birthday: time.Now().Add(-30 * time.Hour * 24 * 365),
				Stack:    []string{"golang"},
			}
		}
	}()

	// go writer(ctx, outC, dbWriter)
	go writer(ctx, outC, dbWriter)
	go writer(ctx, outC, dbWriter)

	// magic time to just wait the work finish
	time.Sleep(10 * time.Second)

	var count int
	if err := conn.QueryRow(context.Background(), "SELECT COUNT(*) FROM person").Scan(&count); err != nil {
		t.Fatal(err)
	}

	if count != max*batchCout*2 {
		t.Fatalf("expected %d, got %d", max*batchCout*2, count)
	}

}
