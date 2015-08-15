package main

import (
	"bufio"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/codegangsta/cli"
	"github.com/lib/pq"
)

func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %s", msg, err)
		panic(fmt.Sprintf("%s: %s", msg, err))
	}
}

func guessSeparator(file *os.File) string {
	scanner := bufio.NewScanner(file)
	separators := []string{",", ";", " ", "\t"}
	separatorCounts := make(map[string]int)
	for scanner.Scan() {
		line := scanner.Text()
		for _, sep := range separators {
			separatorCounts[sep] += strings.Count(line, sep)
		}
	}

	err := scanner.Err()
	failOnError(err, "Could not scan file")

	maxSep := separators[0]
	maxCount := separatorCounts[maxSep]
	for sep, count := range separatorCounts {
		if count > maxCount {
			maxCount = count
			maxSep = sep
		}
	}
	return maxSep
}

func importJson(c *cli.Context) {

}

func connect(connStr string) *sql.DB {
	db, err := sql.Open("postgres", connStr)
	failOnError(err, "Could not prepare connection to database")

	err = db.Ping()
	failOnError(err, "Could not reach the database")

	createSchema, err := db.Prepare("CREATE SCHEMA IF NOT EXISTS import")
	failOnError(err, "Could not create statement")

	_, err = createSchema.Exec()
	failOnError(err, "Could not create schema")

	return db
}

func createConnStr(c *cli.Context) string {
	otherParams := "sslmode=disable connect_timeout=5"
	return fmt.Sprintf("user=%s dbname=%s password='%s' host=%s port=%s %s",
		c.GlobalString("username"),
		c.GlobalString("dbname"),
		c.GlobalString("pass"),
		c.GlobalString("host"),
		c.GlobalString("port"),
		otherParams,
	)
}

func importCsv(c *cli.Context) {
	filename := c.Args().First()
	if filename == "" {
		fmt.Println("Please provide name of file to import")
		os.Exit(1)
	}

	db := connect(createConnStr(c))
	defer db.Close()

	file, err := os.Open(filename)
	failOnError(err, "Cannot open file")
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = rune(guessSeparator(file)[0])
	file.Seek(0, 0)

	columnLengths := make(map[int]int)
	for {
		record, err := reader.Read()

		if err == io.EOF {
			break
		}
		failOnError(err, "Could not read csv")

		for i, column := range record {
			if len(column) > columnLengths[i] {
				columnLengths[i] = len(column)
			}
		}
	}
	file.Seek(0, 0)
	columnTypes := make(map[int](string))

	for colIndex, maxLength := range columnLengths {
		columnTypes[colIndex] = fmt.Sprintf("VARCHAR (%d)", maxLength)
	}

	columns := make([]string, 0)
	columnCreates := make([]string, 0)
	for i := 0; i < len(columnTypes); i++ {
		columnType := columnTypes[i]
		columns = append(columns, fmt.Sprintf("col%d", i))
		columnCreates = append(columnCreates, fmt.Sprintf("col%d %s", i, columnType))
	}
	columnQuery := strings.Join(columnCreates, ",")
	createTable, err := db.Prepare(fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", "import.impiwimpi", columnQuery))
	failOnError(err, "Could not create statement")

	_, err = createTable.Exec()
	failOnError(err, "Could not create table")

	txn, err := db.Begin()
	failOnError(err, "Could not start transaction")

	stmt, err := txn.Prepare(pq.CopyInSchema("import", "impiwimpi", columns...))
	failOnError(err, "Could not prepare copy in statement")

	for {
		record, err := reader.Read()
		cols := make([]interface{}, len(columnTypes))
		for i, b := range record {
			cols[i] = b
		}

		if err == io.EOF {
			break
		}
		failOnError(err, "Could not read csv")
		_, err = stmt.Exec(cols...)
		failOnError(err, "Could add bulk insert")
	}

	_, err = stmt.Exec()
	failOnError(err, "Could not exec the bulk copy")

	err = stmt.Close()
	failOnError(err, "Could not close")

	err = txn.Commit()
	failOnError(err, "Could not commit transaction")

}

func main() {
	app := cli.NewApp()
	app.Name = "pgfutter"
	app.Usage = "Imports anything into PostgreSQL"
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:   "dbname, db",
			Value:  "postgres",
			Usage:  "database to connect to",
			EnvVar: "DB_NAME",
		},
		cli.StringFlag{
			Name:   "host",
			Value:  "localhost",
			Usage:  "host name",
			EnvVar: "DB_HOST",
		},
		cli.StringFlag{
			Name:   "port",
			Value:  "5432",
			Usage:  "port",
			EnvVar: "DB_PORT",
		},
		cli.StringFlag{
			Name:   "username, user",
			Value:  "postgres",
			Usage:  "username",
			EnvVar: "DB_USER",
		},
		cli.StringFlag{
			Name:   "pass, pw",
			Value:  "",
			Usage:  "password",
			EnvVar: "DB_PASS",
		},
	}

	app.Commands = []cli.Command{
		{
			Name:   "json",
			Usage:  "Import lines of JSON objects into database",
			Action: importJson,
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:  "no-cache",
					Usage: "Do not use cache when building the image.",
				},
			},
		},
		{
			Name:   "csv",
			Usage:  "Import CSV into database",
			Action: importCsv,
		},
	}

	app.Run(os.Args)
}
