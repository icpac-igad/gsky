// Metadata API
// Copyright (c) 2017, NCI, Australian National University.

package main

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"

	_ "github.com/lib/pq"
	"github.com/nci/gomemcache/memcache"
)

var (
	db         *sql.DB
	mc         *memcache.Client
	dbHost     = flag.String("dbhost", "/var/run/postgresql", "dbhost")
	dbName     = flag.String("database", "mas", "database name")
	dbUser     = flag.String("user", "api", "database user name")
	dbPassword = flag.String("password", "", "database user password")
	dbPool     = flag.Int("pool", 8, "database pool size")
	dbLimit    = flag.Int("limit", 64, "database concurrent requests")
	httpPort   = flag.Int("port", 8080, "http port")
	mcURI      = flag.String("memcache", "", "memcache uri host:port")
)

// Spit out a simple JSON-formatted error message for Content-Type: application/json
func httpJSONError(response http.ResponseWriter, err error, status int) {
	http.Error(response, fmt.Sprintf(`{ "error": %q }`, err.Error()), status)
}

func handler(response http.ResponseWriter, request *http.Request) {

	response.Header().Set("Content-Type", "application/json")

	var hash string

	if mc != nil {

		buff := md5.Sum([]byte(request.URL.RequestURI()))
		hash = hex.EncodeToString(buff[:])

		if cached, ok := mc.Get(hash); ok == nil {
			response.Write(cached.Value)
			return
		}
	}

	query := request.URL.Query()
	var payload string
	var err error

	if _, ok := query["intersects"]; ok {

		// Use Postgres prepared statements and placeholders for input checks.
		// The nullif() noise is to coerce Go's empty string zero values for
		// missing parameters into proper null arguments.
		// The string_to_array() call will return null in the case of a null
		// argument, rather than array[] or array[null].

		err = db.QueryRow(
			`select mas_intersects(
				nullif($1,'')::text,
				nullif($2,'')::text,
				nullif($3,'')::text,
				nullif($4,'')::integer,
				nullif($5,'')::timestamptz,
				nullif($6,'')::timestamptz,
				string_to_array(nullif($7,''), ','),
				nullif($8,'')::text,
				nullif($9,'')::float8,
				nullif($10,'')::float,
				nullif($11,'')::int
			) as json`,
			request.URL.Path,
			request.FormValue("srs"),
			request.FormValue("wkt"),
			request.FormValue("nseg"),
			request.FormValue("time"),
			request.FormValue("until"),
			request.FormValue("namespace"),
			request.FormValue("metadata"),
			request.FormValue("identitytol"),
			request.FormValue("dptol"),
			request.FormValue("limit"),
		).Scan(&payload)

	} else if _, ok := query["timestamps"]; ok {
		err = db.QueryRow(
			`select mas_timestamps(
				nullif($1,'')::text,
				nullif($2,'')::timestamptz,
				nullif($3,'')::timestamptz,
				string_to_array(nullif($4,''), ','),
				nullif($5,'')::text
			) as json`,
			request.URL.Path,
			request.FormValue("time"),
			request.FormValue("until"),
			request.FormValue("namespace"),
			request.FormValue("token"),
		).Scan(&payload)

	} else if _, ok := query["extents"]; ok {
		err = db.QueryRow(
			`select mas_spatial_temporal_extents(
				nullif($1,'')::text,
				string_to_array(nullif($2,''), ',')
			) as json`,
			request.URL.Path,
			request.FormValue("namespace"),
		).Scan(&payload)

	} else if _, ok := query["list_root_gpath"]; ok {
		err = db.QueryRow(
			`select mas_list_root_gpath() as json`,
		).Scan(&payload)

	} else if _, ok := query["list_sub_gpath"]; ok {
		err = db.QueryRow(
			`select mas_list_sub_gpath(
				nullif($1,'')::text
			) as json`,
			request.URL.Path,
		).Scan(&payload)

	} else if _, ok := query["generate_layers"]; ok {
		err = db.QueryRow(
			`select mas_generate_layers(
				nullif($1,'')::text
			) as json`,
			request.URL.Path,
		).Scan(&payload)

	} else if _, ok := query["put_ows_cache"]; ok {
		err = db.QueryRow(
			`select mas_put_ows_cache(
				nullif($1,'')::text,
        nullif($2,'')::text,
        nullif($3,'')::jsonb
			) as json`,
			request.URL.Path,
			request.FormValue("query"),
			request.FormValue("value"),
		).Scan(&payload)

	} else if _, ok := query["get_ows_cache"]; ok {
		err = db.QueryRow(
			`select mas_get_ows_cache(
				nullif($1,'')::text,
        nullif($2,'')::text
			) as json`,
			request.URL.Path,
			request.FormValue("query"),
		).Scan(&payload)

	} else {
		httpJSONError(response, errors.New("unknown operation; currently supported: ?intersects, ?timestamps, ?extents"), 400)
		return
	}

	if err != nil {
		httpJSONError(response, err, 400)
		return
	}

	response.Write([]byte(payload))

	if mc != nil {
		// don't care about errors; memcache may not necessarily retain this anyway
		mc.Set(&memcache.Item{Key: hash, Value: []byte(payload)})
	}

}

func main() {

	flag.Parse()

	log.Printf("dbHost %s dbUser %s dbName %s dbPool %d httpPort %d", *dbHost, *dbUser, *dbName, *dbPool, *httpPort)

	dbinfo := fmt.Sprintf("user=%s host=%s dbname=%s sslmode=disable", *dbUser, *dbHost, *dbName)

	if *dbPassword != "" {
		dbinfo = fmt.Sprintf("%s password=%s", dbinfo, *dbPassword)
	}

	var err error
	db, err = sql.Open("postgres", dbinfo)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	// sql.Open() does lazy evaluation. Here we do some simple
	// test to assert if connection is okay.
	var payload string
	err = db.QueryRow("select true").Scan(&payload)
	if err != nil {
		panic(err)
	}

	db.SetMaxIdleConns(*dbPool)
	db.SetMaxOpenConns(*dbLimit)

	if *mcURI != "" {
		// lazy connection; errors returned in .Get
		mc = memcache.New(*mcURI)
	}

	http.HandleFunc("/", handler)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *httpPort), nil))
}
