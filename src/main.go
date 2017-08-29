package main

import (
  "fmt"
  //"time"
  "flag"
  "os"
  "strings"
  "errors"
  "golang.org/x/net/context"
  //vegeta "github.com/tsenart/vegeta/lib"
  pb "github.com/lyft/ratelimit/proto/ratelimit"
  "google.golang.org/grpc"
  //"github.com/tsenart/vegeta/lib"
  "time"
  "github.com/tsenart/vegeta/lib"
  "net/http"
  "github.com/montanaflynn/stats"
  "sync"
  "bytes"
  "sync/atomic"
)

var latencies = []float64{}
var mux sync.Mutex

type descriptorValue struct {
  descriptor *pb.RateLimitDescriptor
}

func (this *descriptorValue) Set(arg string) error {
  pairs := strings.Split(arg, ",")
  for _, pair := range pairs {
    parts := strings.Split(pair, "=")
    if len(parts) != 2 {
      return errors.New("invalid descriptor list")
    }

    this.descriptor.Entries = append(
      this.descriptor.Entries, &pb.RateLimitDescriptor_Entry{Key: parts[0], Value: parts[1]})
  }

  return nil
}

func (this *descriptorValue) String() string {
  return this.descriptor.String()
}

func main() {
  dialString := flag.String(
    "dial_string", "35.188.184.140:8081", "url of ratelimit server in <host>:<port> form")
  domain := flag.String("domain", "acxiom", "rate limit configuration domain to query")
  entries := []*pb.RateLimitDescriptor_Entry{{Key:"max_rate",Value:"users"}}
  descriptors := []*pb.RateLimitDescriptor{{Entries: entries}}
  descriptorValue := descriptorValue{&pb.RateLimitDescriptor{Entries: entries}}
  flag.Var(
    &descriptorValue, "descriptors",
    "descriptor list to query in <key>=<value>,<key>=<value>,... form")
  flag.Parse()

  fmt.Printf("dial string: %s\n", *dialString)
  fmt.Printf("domain: %s\n", *domain)
  fmt.Printf("descriptors: %s\n", &descriptorValue)

  conn, err := grpc.Dial(*dialString, grpc.WithInsecure())
  if err != nil {
    fmt.Printf("error connecting: %s\n", err.Error())
    os.Exit(1)
  }

  defer conn.Close()
  c := pb.NewRateLimitServiceClient(conn)

  for i := 0; i < 10; i++ {
    go doRequests(c, domain, descriptors)
  }
  for {
    print()
  }
}

var i int64 = 0

func doRequests(c pb.RateLimitServiceClient, domain *string, descriptors []*pb.RateLimitDescriptor) {
  for {
    if !isRateLimited(c, domain, descriptors) {
      start := time.Now()

      var jsonStr = []byte(`{"method": "simple","proxy": "computeip","url": "http://www.httpbin.org/delay/102000","validCacheAge": 0}`)
      http.Post("https://edge-test.vendasta-internal.com:22000/api/v1/url/get", "application/x-www-form-urlencoded", bytes.NewBuffer(jsonStr))

      end := time.Now()
      latency := end.Sub(start)

      mux.Lock()
      latencies = append(latencies, latency.Seconds())
      mux.Unlock()

      //fmt.Println("OK")
    } else {
      fmt.Println("Limited")
    }
    median, _ := stats.Median(latencies)
    mean, _ := stats.Mean(latencies)
    percentile, _ := stats.Percentile(latencies, 95)
    if i > 10 {
      fmt.Println("Median: ", median)
      fmt.Println("Mean: ", mean)
      fmt.Println("95th Percentile: ", percentile)
      atomic.StoreInt64(&i, 0)
    }
    atomic.AddInt64(&i, 1)
  }
}

func isRateLimited(c pb.RateLimitServiceClient, domain *string, descriptors []*pb.RateLimitDescriptor) bool {
  response, err := c.ShouldRateLimit(
    context.Background(),
    &pb.RateLimitRequest{*domain, descriptors, 1})
  if err != nil {
    fmt.Printf("request error: %s\n", err.Error())
    os.Exit(1)
  }
  allOk := true
  for _, res := range response.Statuses {
    if res.Code != pb.RateLimitResponse_OK {
      allOk = false
    }
  }
  return !allOk
}

func attack() {
  currRate := 10
  for currRate < 200 {
    rate := uint64(currRate) // per second
    duration := 5 * time.Second
    targeter := vegeta.NewStaticTargeter(
      vegeta.Target {
        Method: "GET",
        URL:    "https://acxiom-data-transfer.vendasta-internal.com/",
      },
    )
    attacker := vegeta.NewAttacker()

    var metrics vegeta.Metrics
    results := vegeta.Results{}
    for res := range attacker.Attack(targeter, rate, duration) {
      metrics.Add(res)
      results.Add(res)
    }
    metrics.Close()

    fmt.Printf("Current Rate: %v\n", currRate)
    fmt.Printf("Rate: %v\n", metrics.Rate)
    fmt.Printf("Duration: %v\n", metrics.Duration)
    fmt.Printf("Mean: %v\n", metrics.Latencies.Mean)
    fmt.Printf("P95: %v\n", metrics.Latencies.P95)
    fmt.Printf("Status Codes: %v\n", metrics.StatusCodes)
    fmt.Printf("Errors: %v\n", metrics.Errors)
    fmt.Printf("Success: %v\n", metrics.Success)
    fmt.Printf("\n\n")
    if metrics.StatusCodes["200"] < currRate * 5 - 50 {
      break
    }
    currRate += 1
  }
}