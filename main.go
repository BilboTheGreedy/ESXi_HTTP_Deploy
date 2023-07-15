package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/KarpelesLab/iso9660/isoutil"
	"github.com/gorilla/mux"
)

type Image struct {
	Root_Path    string
	ISO_Path     string
	Extract_Path string
	Extract_Name string
}

type Http_server struct {
	Port    string
	Address string
}
type Color string

type key int

const (
	requestIDKey key = 0
)

var (
	listenAddr string
	healthy    int32
)

const (
	ColorBlack  = "\u001b[30m"
	ColorRed    = "\u001b[31m"
	ColorGreen  = "\u001b[32m"
	ColorYellow = "\u001b[33m"
	ColorBlue   = "\u001b[34m"
	ColorReset  = "\u001b[0m"
)

func colorize(color Color, message string) {
	fmt.Println(string(color), message, string(ColorReset))
}
func GetOutboundIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP
}

var iso_path string
var name string
var port string
var ks bool

func main() {

	flag.StringVar(&port, "port", "8081", "port")
	flag.StringVar(&iso_path, "iso", "", "full path to vmware installer media")
	flag.StringVar(&name, "name", "", "Extract name of ISO content to http dir")
	flag.BoolVar(&ks, "ks", false, "set kernalopts to kickstart file")
	flag.Parse()
	ks_sample := []string{
		"vmaccepteula",
		"rootpw !PassW0rd",
		"install --firstdisk --overwritevmfs",
		"network --bootproto=dhcp --device=vmnic0",
	}

	colorize(ColorGreen, iso_path)

	image := Image{
		Root_Path:    "http",
		ISO_Path:     iso_path,
		Extract_Path: "default_media",
		Extract_Name: name,
	}
	http_server := Http_server{
		Port:    ":" + port,
		Address: GetOutboundIP().String(),
	}
	writeKsSample(ks_sample, image.Root_Path+"/ks/ks.cfg")
	fmt.Println("Running webserver on:", GetOutboundIP().String()+http_server.Port+"\n")
	CreateDirIfNotExist((filepath.Join(image.Root_Path, image.Extract_Path)))
	CreateDirIfNotExist((filepath.Join(image.Root_Path, "/ks")))
	extractISO(image.ISO_Path, (filepath.Join(image.Root_Path, image.Extract_Path, image.Extract_Name)))
	CopyBootCFG((filepath.Join(image.Root_Path, image.Extract_Path, image.Extract_Name)), image.Root_Path)
	formatBootCFG(image.Root_Path+"/boot.cfg", http_server.Address+http_server.Port+"/"+image.Extract_Path+"/"+image.Extract_Name, "runweasel", "Loading ESXi installer from HTTP Server", http_server)
	CopyEFIBootFile((filepath.Join(image.Root_Path, image.Extract_Path, image.Extract_Name)), image.Root_Path)
	nextRequestID := func() string {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	logger := log.New(os.Stdout, "http: ", log.LstdFlags)
	logger.Println("Server is starting...")
	router := mux.NewRouter()
	router.HandleFunc("/test", TestEndpoint).Methods("GET")
	router.PathPrefix("/").Handler(http.StripPrefix("/", http.FileServer(http.Dir(image.Root_Path))))
	srv := &http.Server{
		Addr:    http_server.Port,
		Handler: tracing(nextRequestID)(logging(logger)(router)),
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()
	log.Print("Server Started")

	<-done
	log.Print("Server Stopped")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer func() {
		// extra handling here
		cancel()
	}()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server Shutdown Failed:%+v", err)
	}
	log.Print("Server Exited Properly")
}

func TestEndpoint(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)

	w.Write([]byte("Test endpoint"))
}

func runHTTPServer(http_dir string, port string) {
	http.Handle("/", http.FileServer(http.Dir(http_dir)))

	fmt.Println("Listening on port: ", port)
	http.ListenAndServe(port, nil)
}

func CreateDirIfNotExist(dir string) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0755)
		if err != nil {
			panic(err)
		}
	}
}

func writeKsSample(ks_sample []string, output_path string) {
	if _, err := os.Stat(output_path); err == nil {
		colorize(ColorYellow, "kickstart example file exists\n")
	} else {
		file, err := os.OpenFile(output_path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)

		if err != nil {
			log.Fatalf("failed creating file: %s", err)
		}

		datawriter := bufio.NewWriter(file)

		for _, data := range ks_sample {
			_, _ = datawriter.WriteString(data + "\n")
		}

		datawriter.Flush()
		file.Close()
	}

}

func extractISO(source_path string, output_path string) {
	colorize(ColorGreen, "Extract ISO")
	f, err := os.Open(source_path)
	if err != nil {
		log.Fatalf("failed to open file: %s", err)
	}
	defer f.Close()

	if err = isoutil.ExtractImageToDirectory(f, output_path); err != nil {
		log.Fatalf("failed to extract image: %s", err)
	}

}

func formatBootCFG(source_path string, prefix string, kernelopt string, title string, http_server Http_server) {

	colorize(ColorGreen, "Configure boot.cfg file")

	file, err := os.Open(source_path)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.ReplaceAll(line, "/", "")
		if strings.HasPrefix(line, "prefix=") {
			fmt.Println("Setting prefix to:", prefix)
			line = fmt.Sprintf("%s%s", line, "http://"+prefix)
		}
		if strings.HasPrefix(line, "kernelopt=") {
			parts := strings.Split(line, "=")
			if len(parts) > 1 {
				if ks == true {
					fmt.Println("Setting kernelopt to:", "ks=http://"+http_server.Address+http_server.Port+"/ks/ks.cfg")
					line = fmt.Sprintf("%s=%s", parts[0], "ks=http://"+http_server.Address+http_server.Port+"/ks/ks.cfg")
				} else {
					fmt.Println("Setting kernelopt to:", kernelopt)
					line = fmt.Sprintf("%s=%s", parts[0], kernelopt)
				}

			}
		}
		if strings.HasPrefix(line, "title=") {
			parts := strings.Split(line, "=")
			if len(parts) > 1 {
				fmt.Println("Setting title to:", title)
				line = fmt.Sprintf("%s=%s", parts[0], title)
			}
		}
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		fmt.Println(err)
		return
	}

	// Write the updated contents back to the file.
	f, err := os.Create(source_path)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}

	if err = w.Flush(); err != nil {
		fmt.Println(err)
		return
	}
}

func CopyEFIBootFile(source_path string, output_path string) {
	originalFile, err := os.Open(source_path + "/EFI/BOOT/BOOTX64.EFI")
	if err != nil {
		panic(err)
	}
	defer originalFile.Close()

	newFile, err := os.Create(output_path + "/mboot.efi")
	if err != nil {
		panic(err)
	}
	defer newFile.Close()

	_, err = io.Copy(newFile, originalFile)
	if err != nil {
		panic(err)
	}
}

func CopyBootCFG(source_path string, output_path string) {
	originalFile, err := os.Open(source_path + "/BOOT.CFG")
	if err != nil {
		panic(err)
	}
	defer originalFile.Close()

	newFile, err := os.Create(output_path + "/boot.cfg")
	if err != nil {
		panic(err)
	}
	defer newFile.Close()

	_, err = io.Copy(newFile, originalFile)
	if err != nil {
		panic(err)
	}
}

func healthz() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.LoadInt32(&healthy) == 1 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
}

func logging(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				requestID, ok := r.Context().Value(requestIDKey).(string)
				if !ok {
					requestID = "unknown"
				}
				logger.Println(requestID, r.Method, r.URL.Path, r.RemoteAddr, r.UserAgent())
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func tracing(nextRequestID func() string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get("X-Request-Id")
			if requestID == "" {
				requestID = nextRequestID()
			}
			ctx := context.WithValue(r.Context(), requestIDKey, requestID)
			w.Header().Set("X-Request-Id", requestID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
