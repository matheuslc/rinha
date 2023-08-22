package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
)

var userCache = sync.Map{}

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
}

type reader interface {
	find(ctx context.Context, id uuid.UUID) (*person, error)
	search(ctx context.Context, term string) ([]*person, error)
	count(ctx context.Context) (int, error)
}

func main() {
	ctx := context.Background()
	cfgConn, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to parse DATABASE_URL: %v\n", err)
		os.Exit(1)
	}

	cfgConn.MaxConns = 10
	cfgConn.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement

	conn, err := pgxpool.NewWithConfig(ctx, cfgConn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to database: %v\n", err)
		os.Exit(1)
	}

	defer conn.Close()

	repoWriter := &repo{conn: conn}
	repoReader := &readerRepo{conn: conn}

	parallel := 30
	duration := 5 * time.Second
	batchSize := 1000
	inC := make(chan *person, 100000)
	batcherOutc := make(chan []*person, 1000)

	for i := 0; i < parallel; i++ {
		go batcher(ctx, inC, batcherOutc, batchSize, duration)
	}

	for i := 0; i < parallel; i++ {
		go writer(ctx, batcherOutc, repoWriter)
	}

	router := echo.New()
	router.POST("/pessoas", createUserHandler(inC))
	router.GET("/pessoas", searchPerson(repoReader))
	router.GET("/pessoas/:id", getPerson(repoReader))
	router.GET("/contagem-pessoas", count(repoReader))

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

func (r *readerRepo) count(ctx context.Context) (int, error) {
	var count int
	row := r.conn.QueryRow(ctx, "SELECT count(1) FROM person")
	err := row.Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

func (r *readerRepo) search(ctx context.Context, term string) ([]*person, error) {
	rows, err := r.conn.Query(
		context.Background(),
		"SELECT uuid, name, nickname, birthday, stack FROM person WHERE search LIKE '%' || $1 || '%'",
		term,
	)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var people []*person
	for rows.Next() {
		var t time.Time
		var stack string
		u := &person{}

		err := rows.Scan(&u.UUID, &u.Name, &u.Nickname, &t, &stack)
		if err != nil {
			return nil, err
		}

		u.Birthday = t.Format("2006-01-02")
		u.Stack = strings.Split(stack, ",")
		people = append(people, u)
	}

	return people, nil
}

func (r *repo) run(ctx context.Context, p []*person) error {
	batch := &pgx.Batch{}

	for _, person := range p {
		bday, err := time.Parse("2006-01-02", person.Birthday)
		if err != nil {
			return err
		}

		stack := strings.Join(person.Stack, ",")
		batch.Queue(
			"INSERT INTO person(uuid, name, nickname, birthday, stack) VALUES($1, $2, $3, $4, $5)",
			person.UUID,
			person.Name,
			person.Nickname,
			bday,
			stack,
		)
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
		[]string{"uuid", "name", "nickname", "birthday", "stack"},
		pgx.CopyFromSlice(len(p), func(i int) ([]any, error) {
			t, _ := time.Parse("2006-01-02", p[i].Birthday)
			stack := strings.Join(p[i].Stack, ",")

			return []any{
				p[i].UUID,
				p[i].Name,
				p[i].Nickname,
				t,
				stack,
			}, nil
		}),
	)

	if err != nil {
		return err
	}

	return nil
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

func createUserHandler(inC chan *person) func(c echo.Context) error {
	return func(c echo.Context) error {
		u := person{}
		if err := c.Bind(&u); err != nil {
			return c.String(http.StatusBadRequest, "invalid request")
		}

		u.UUID = uuid.New()
		inC <- &u

		// Loading new user into cache
		userCache.Store(u.UUID.String(), u)

		c.Response().Header().Set("Location", fmt.Sprintf("/pessoas/%s", u.UUID))

		return c.String(http.StatusCreated, "ok")
	}
}

func getPerson(reader reader) func(c echo.Context) error {
	return func(c echo.Context) error {
		id := c.Param("id")
		idParsed := uuid.MustParse(id)

		// let's look into the in memory cache first
		if u, ok := userCache.Load(id); ok {
			return c.JSON(http.StatusOK, u)
		}

		// Here, we check if the request hasn't already been forwarded
		// and if it is wasn't, we check the cache again. Otherwise, we will query the database
		if c.Request().Header.Get("x-forwarded") == "" {
			c.Response().Header().Set("x-forwarded", "true")
			return c.String(http.StatusNotFound, "Not Found")
		}

		u, err := reader.find(context.Background(), idParsed)
		if err != nil {
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

		u, err := reader.search(context.Background(), term)
		if err != nil {
			fmt.Println("search error", err)
			return c.String(http.StatusNotFound, "got error")
		}

		return c.JSON(http.StatusOK, u)
	}
}

func count(r reader) func(c echo.Context) error {
	return func(c echo.Context) error {
		count, err := r.count(context.Background())
		if err != nil {
			return c.String(http.StatusNotFound, "Not Found")
		}

		return c.JSON(http.StatusOK, count)
	}
}

// empty is a helper function to just return a 200 OK to any unimplemented endpoint
// it's useful to isolate test cases
func empty(c echo.Context) error {
	return c.String(http.StatusOK, "ok")
}
