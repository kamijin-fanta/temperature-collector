package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/go-ble/ble"
	"github.com/go-ble/ble/examples/lib/dev"
	"github.com/go-ble/ble/linux"
	"github.com/go-ble/ble/linux/hci/cmd"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"log"
	"net/http"
	"time"
)

func main() {
	var listen = flag.String("listen", ":2112", "metrics listen address")
	flag.Parse()

	// https://github.com/go-ble/ble/tree/master/examples

	d, err := dev.NewDevice("default")
	if err != nil {
		log.Fatalf("can't new device : %s", err)
	}
	ble.SetDefaultDevice(d)
	dev := d.(*linux.Device)

	if err := dev.HCI.Send(&cmd.LESetScanParameters{
		LEScanType:           0x01,   // 0x00: passive, 0x01: active
		LEScanInterval:       0x0004, // 0x0004 - 0x4000; N * 0.625msec
		LEScanWindow:         0x0004, // 0x0004 - 0x4000; N * 0.625msec
		OwnAddressType:       0x01,   // 0x00: public, 0x01: random
		ScanningFilterPolicy: 0x00,   // 0x00: accept all, 0x01: ignore non-white-listed.
	}, nil); err != nil {
		panic(err)
	}

	collector := &SwitchBotCollector{}
	prometheus.MustRegister(collector)

	ctx, cancel := context.WithCancel(context.TODO())
	go func() {
		http.Handle("/metrics", promhttp.Handler())

		fmt.Printf("listen in %s\n", *listen)
		err := http.ListenAndServe(*listen, nil)
		if err != nil {
			panic(err)
		}
		cancel()
	}()

	ble.Scan(ctx, true, advHandler, nil)
}

var deviceStatuses map[string]DeviceStatus = map[string]DeviceStatus{}

type DeviceStatus struct {
	Temperature float64
	Humidity    int
	Battery     int
	Updated     time.Time
}

func advHandler(a ble.Advertisement) {
	found := false
	for _, uuid := range a.Services() {
		if uuid.String() == "cba20d00224d11e69fb80002a5d5c51b" {
			found = true
		}
	}
	if !found {
		return
	}

	for _, data := range a.ServiceData() {
		temp := float64(data.Data[4] & 0x7f)
		temp += float64(data.Data[3]) / 10
		humidity := int(data.Data[5] & 0x7f)
		battery := int(data.Data[2])

		fmt.Printf("[%s] temp: %.1f humidity: %d battery: %d\n", a.Addr(), temp, humidity, battery)

		deviceStatuses[a.Addr().String()] = DeviceStatus{
			Temperature: temp,
			Humidity:    humidity,
			Battery:     battery,
			Updated:     time.Now(),
		}
	}
}

var ns = "temperature_collector"

type SwitchBotCollector struct {
}

func (*SwitchBotCollector) Describe(chan<- *prometheus.Desc) {
}

func (*SwitchBotCollector) Collect(ch chan<- prometheus.Metric) {
	current := time.Now()
	for addr, status := range deviceStatuses {
		if current.Sub(status.Updated) > 1*time.Minute {
			continue
		}
		labels := map[string]string{
			"hw": addr,
		}
		tmpGauge := prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace:   ns,
			Name:        "temperature",
			ConstLabels: labels,
		})
		tmpGauge.Set(status.Temperature)

		humGauge := prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace:   ns,
			Name:        "humidity",
			ConstLabels: labels,
		})
		humGauge.Set(float64(status.Humidity))

		batteryGauge := prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace:   ns,
			Name:        "battery",
			ConstLabels: labels,
		})
		batteryGauge.Set(float64(status.Battery))

		ch <- tmpGauge
		ch <- humGauge
		ch <- batteryGauge
	}
}
