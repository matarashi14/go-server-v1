package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/umahmood/haversine"

	_ "github.com/lib/pq"
)

// Structs to parse the response of the external API
type Location struct {
	City       string `json:"city"`
	Town       string `json:"town"`
	X          string `json:"x"`
	Y          string `json:"y"`
	Prefecture string `json:"prefecture"`
	Postal     string `json:"postal"`
}

type Response struct {
	Location []Location `json:"location"`
}

type APIResponse struct {
	Response Response `json:"response"`
}

// Struct to form the response of our API
type AppResponse struct {
	PostalCode       string  `json:"postal_code"`
	HitCount         int     `json:"hit_count"`
	Address          string  `json:"address"`
	TokyoStaDistance float64 `json:"tokyo_sta_distance"`
}

var tokyoX = 139.7673068
var tokyoY = 35.6809591

type AppConfig struct {
	DBHost   string
	DBName   string
	Password string
}

func main() {

	// Load .env file
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	config := AppConfig{
		DBHost:   os.Getenv("DB_HOST"),
		DBName:   os.Getenv("DB_NAME"),
		Password: os.Getenv("PASSWORD"),
	}

	// Create database connection
	connStr, err := dbConnStr(config)
	if err != nil {
		log.Fatal(err)
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer func() {
		err := db.Close()
		if err != nil {
			fmt.Println("Error closing database:", err.Error())
		}
	}()

	e := echo.New()

	// Middleware functions for logging and error recovery
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	// Routes
	e.GET("/address", func(c echo.Context) error {
		return getAddress(c, db)
	})
	e.GET("/address/access_logs", func(c echo.Context) error {
		return getAccessLogs(c, db)
	})

	// Start the server
	e.Start(":8080")
}

func dbConnStr(config AppConfig) (string, error) {

	// Database connection string
	dbHost := config.DBHost
	dbName := config.DBName

	password := config.Password
	if password == "" {
		return "", errors.New("missing or invalid database password")
	}
	connStr := fmt.Sprintf("host=%s user=postgres password=%s dbname=%s sslmode=disable", dbHost, password, dbName)

	return connStr, nil
}

// Handler function for the /address route
func getAddress(c echo.Context, db *sql.DB) error {

	// Get the postal code from the query parameters
	postalCode := c.QueryParam("postal_code")

	// Insert a record in the access_logs table for this requests
	_, err := db.Exec("INSERT INTO access_logs (postal_code, created_at) VALUES ($1, $2)", postalCode, time.Now())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	url := fmt.Sprintf("https://geoapi.heartrails.com/api/json?method=searchByPostal&postal=%s", postalCode)

	// Send a GET request to the external API
	resp, err := http.Get(url)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	var apiResponse APIResponse
	err = json.Unmarshal(body, &apiResponse)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	// Find the shortest address and the maximum distance to Tokyo Station
	var shortestAddress string
	addresses := make([]string, len(apiResponse.Response.Location))
	for i, location := range apiResponse.Response.Location {
		addresses[i] = location.Prefecture + location.City + location.Town
		if shortestAddress == "" || len(addresses[i]) < len(shortestAddress) {
			shortestAddress = addresses[i]
		}
	}

	// Calculate the distance to Tokyo Station
	maxDistance := 0.0
	for _, location := range apiResponse.Response.Location {
		x, _ := strconv.ParseFloat(location.X, 64)
		y, _ := strconv.ParseFloat(location.Y, 64)

		_, km := haversine.Distance(haversine.Coord{Lat: tokyoY, Lon: tokyoX}, haversine.Coord{Lat: y, Lon: x})

		if km > maxDistance {
			maxDistance = km
		}
	}

	appResponse := AppResponse{
		PostalCode:       postalCode,
		HitCount:         len(apiResponse.Response.Location),
		Address:          CommonPrefix(addresses),
		TokyoStaDistance: math.Round(maxDistance*10) / 10,
	}

	// Return the response as JSON
	return c.JSON(http.StatusOK, appResponse)
}

// Function to find the common prefix among an array of strings
func CommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return "Error: func commonPrefix"
	}

	prefix := []rune(strs[0])

	for _, str := range strs {
		strRune := []rune(str)
		if len(strRune) < len(prefix) {
			prefix = prefix[:len(strRune)]
		}
		for i := range prefix {
			if prefix[i] != strRune[i] {
				prefix = prefix[:i]
				break
			}
		}
	}

	if len(prefix) == 0 {
		return "None"
	}
	return string(prefix)
}

// Struct to parse the rows of the access_logs table
type AccessLog struct {
	PostalCode   string `json:"postal_code"`
	RequestCount int    `json:"request_count"`
}

// Handler function for the /address/access_logs route
func getAccessLogs(c echo.Context, db *sql.DB) error {

	rows, err := db.Query("SELECT postal_code, COUNT(*) AS request_count FROM access_logs GROUP BY postal_code ORDER BY request_count DESC")
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	var logs []AccessLog
	for rows.Next() {
		var log AccessLog
		if err := rows.Scan(&log.PostalCode, &log.RequestCount); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		logs = append(logs, log)
	}

	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{"access_logs": logs})
}
