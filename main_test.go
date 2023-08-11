package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestBatcher(t *testing.T) {
	max := 10
	batchCout := 10
	ctx := context.Background()
	inC := make(chan *person, 100)
	outC := make(chan []*person, 100)

	go batcher(ctx, inC, outC, max, 1*time.Second)

	go func() {
		for i := 0; i <= max*batchCout; i++ {
			inC <- &person{
				name:     "test",
				nickname: "test-nickame",
				birthday: time.Now().Add(-30 * time.Hour * 24 * 365),
				stack:    []string{"golang"},
			}
		}
	}()

	go func() {
		for i := 0; i <= max*batchCout; i++ {
			inC <- &person{
				name:     "test",
				nickname: "test-nickame",
				birthday: time.Now().Add(-30 * time.Hour * 24 * 365),
				stack:    []string{"golang"},
			}
		}
	}()

	final := [][]*person{}
	go func() {
		for {
			select {
			case <-ctx.Done():
				fmt.Println("done")
			case p := <-outC:
				final = append(final, p)
			}
		}
	}()

	// magic time to just wait the work finish
	time.Sleep(2 * time.Second)
	if len(final) != batchCout*2 {
		t.Fatalf("not enough batches. got %d, want %d", len(final), batchCout*2)
	}
}
