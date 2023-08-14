package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
)

type person struct {
	UUID     uuid.UUID `json:"id"`
	Name     string    `json:"nome"`
	Nickname string    `json:"apelido"`
	Birthday string    `json:"nascimento"`
	Stack    []string  `json:"stack"`
}

type repo struct {
	conn *pgxpool.Pool
}

type readerRepo struct {
	conn *pgxpool.Pool
}

type copyRepo struct {
	conn *pgxpool.Pool
}

type dbWriter interface {
	run(ctx context.Context, p []*person) error
	single(ctx context.Context, p *person) error
}

type reader interface {
	find(ctx context.Context, id uuid.UUID) (*person, error)
	search(ctx context.Context, term string) ([]*person, error)
}

func main() {
	ctx := context.Background()
	conn, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to database: %v\n", err)
		os.Exit(1)
	}

	defer conn.Close()

	// dbWriter := &copyRepo{conn: conn}
	repoWriter := &repo{conn: conn}
	repoReader := &readerRepo{conn: conn}

	// duration := 5 * time.Millisecond
	// batchSize := 1000
	// inC := make(chan *person, 10000)
	// batcherOutc := make(chan []*person, 10)

	// Spawning 4 batch workers
	// go batcher(ctx, inC, batcherOutc, batchSize, duration)
	// go batcher(ctx, inC, batcherOutc, batchSize, duration)
	// go batcher(ctx, inC, batcherOutc, batchSize, duration)

	// Spawning 2 db workers
	// go writer(ctx, batcherOutc, repoWriter)
	// go writer(ctx, batcherOutc, repoWriter)
	// go singleWriter(ctx, inC, repoWriter)
	// go singleWriter(ctx, inC, repoWriter)

	router := echo.New()
	router.POST("/pessoas", createUserHandler(*repoWriter))
	router.GET("/pessoas", searchPerson(repoReader))
	router.GET("/pessoas/:id", getPerson(repoReader))
	// router.GET("/contagem-pessoas", getPerson)

	router.Logger.Fatal(router.Start("0.0.0.0:80"))
}

func (r *readerRepo) find(ctx context.Context, id uuid.UUID) (*person, error) {
	row := r.conn.QueryRow(ctx, "SELECT uuid, name, nickname, birthday FROM person WHERE uuid = $1", id)
	var t time.Time
	u := &person{}

	err := row.Scan(&u.UUID, &u.Name, &u.Nickname, &t)
	if err != nil {
		return nil, err
	}

	u.Birthday = t.Format("2006-01-02")
	return u, nil
}

func (r *readerRepo) search(ctx context.Context, term string) ([]*person, error) {
	rows, err := r.conn.Query(context.Background(), "SELECT uuid, name, nickname, birthday FROM person WHERE name LIKE $1 or nickname LIKE $1", "%"+term+"%")
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var people []*person
	for rows.Next() {
		var t time.Time
		u := &person{}
		err := rows.Scan(&u.UUID, &u.Name, &u.Nickname, &t)
		if err != nil {
			return nil, err
		}

		u.Birthday = t.Format("2006-01-02")
		people = append(people, u)
	}

	return people, nil
}

func (r *repo) run(ctx context.Context, p []*person) error {
	batch := &pgx.Batch{}

	for _, person := range p {
		bday, _ := time.Parse("2006-01-02", person.Birthday)
		batch.Queue("INSERT INTO person(uuid, name, nickname, birthday) VALUES($1, $2, $3, $4)", person.UUID, person.Name, person.Nickname, bday)
	}

	br := r.conn.SendBatch(ctx, batch)
	if err := br.Close(); err != nil {
		log.Fatal("could not release batch connection. err: ", err)
	}

	return nil
}

func (r *repo) single(ctx context.Context, p *person) error {
	bday, _ := time.Parse("2006-01-02", p.Birthday)
	_, err := r.conn.Exec(ctx, "INSERT INTO person(uuid, name, nickname, birthday) VALUES($1, $2, $3, $4)", p.UUID, p.Name, p.Nickname, bday)
	if err != nil {
		return err
	}

	return nil
}

func (r *copyRepo) run(ctx context.Context, p []*person) error {
	_, err := r.conn.CopyFrom(
		ctx,
		pgx.Identifier{"person"},
		[]string{"uuid", "name", "nickname", "birthday"},
		pgx.CopyFromSlice(len(p), func(i int) ([]any, error) {
			t, _ := time.Parse("2006-01-02", p[i].Birthday)
			return []any{
				p[i].UUID,
				p[i].Name,
				p[i].Nickname,
				t,
			}, nil
		}),
	)

	if err != nil {
		return err
	}

	return nil
}

func singleWriter(ctx context.Context, inC chan *person, w dbWriter) {
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-inC:
			w.single(ctx, p)
		}
	}
}

func writer(ctx context.Context, inC chan []*person, w dbWriter) {
	for {
		select {
		case <-ctx.Done():
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
			outC <- batch

			reset()
		}
	}
}

func createUserHandler(writer repo) func(c echo.Context) error {
	return func(c echo.Context) error {
		u := person{}
		if err := c.Bind(&u); err != nil {
			return c.String(http.StatusBadRequest, "invalid request")
		}

		u.UUID = uuid.New()
		writer.single(context.Background(), &u)

		c.Response().Header().Set("Location", fmt.Sprintf("/pessoas/%s", u.UUID))

		return c.String(http.StatusCreated, "ok")
	}
}

func getPerson(reader reader) func(c echo.Context) error {
	return func(c echo.Context) error {
		id := c.Param("id")
		idParsed := uuid.MustParse(id)
		u, err := reader.find(context.Background(), idParsed)
		if err != nil {
			fmt.Println("id", id)
			fmt.Println("err", err)
			return c.String(http.StatusNotFound, "Not Found")
		}

		return c.JSON(http.StatusOK, u)
	}
}

func searchPerson(reader reader) func(c echo.Context) error {
	return func(c echo.Context) error {
		term := c.QueryParam("t")

		if term == "" {
			return c.String(http.StatusBadRequest, "invalid request")
		}

		// return c.JSON(http.StatusOK, "ok")

		u, err := reader.search(context.Background(), term)
		if err != nil {
			return c.String(http.StatusNotFound, "got error")
		}

		return c.JSON(http.StatusOK, u)
	}
}

func empty(c echo.Context) error {
	return c.String(http.StatusOK, "ok")
}
