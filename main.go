// Copyright 2022 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	kingpin "github.com/alecthomas/kingpin/v2"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promlog"
	"github.com/prometheus/common/promlog/flag"
	"github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	webflag "github.com/prometheus/exporter-toolkit/web/kingpinflag"
)

// Device
type Device struct {
	Name      string `json:"name"`
	Info_Name string `json:"info_name"`
	Type      string `json:"type"`
}

// SMARTctlManagerCollector implements the Collector interface.
type SMARTctlManagerCollector struct {
	CollectPeriod         string
	CollectPeriodDuration time.Duration
	Devices               []Device

	logger log.Logger
	mutex  sync.Mutex
}

const CcissType = "cciss"
const MegaraidType = "megaraid"

// Describe sends the super-set of all possible descriptors of metrics
func (i *SMARTctlManagerCollector) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(i, ch)
}

// Collect is called by the Prometheus registry when collecting metrics.
func (i *SMARTctlManagerCollector) Collect(ch chan<- prometheus.Metric) {
	info := NewSMARTctlInfo(ch)
	i.mutex.Lock()
	for _, device := range i.Devices {
		json := readData(i.logger, device)
		if json.Exists() {
			info.SetJSON(json)
			smart := NewSMARTctl(i.logger, json, ch)
			smart.Collect()
		}
	}
	ch <- prometheus.MustNewConstMetric(
		metricDeviceCount,
		prometheus.GaugeValue,
		float64(len(i.Devices)),
	)
	info.Collect()
	i.mutex.Unlock()
}

func (i *SMARTctlManagerCollector) RescanForDevices() {
	for {
		time.Sleep(*smartctlRescanInterval)
		level.Info(i.logger).Log("msg", "Rescanning for devices")
		devices := scanDevices(i.logger)
		i.mutex.Lock()
		i.Devices = devices
		i.mutex.Unlock()
	}
}

var (
	smartctlPath = kingpin.Flag("smartctl.path",
		"The path to the smartctl binary",
	).Default("/usr/sbin/smartctl").String()
	smartctlInterval = kingpin.Flag("smartctl.interval",
		"The interval between smartctl polls",
	).Default("60s").Duration()
	smartctlRescanInterval = kingpin.Flag("smartctl.rescan",
		"The interval between rescanning for new/disappeared devices. If the interval is smaller than 1s no rescanning takes place. If any devices are configured with smartctl.device also no rescanning takes place.",
	).Default("10m").Duration()
	smartctlDevices = kingpin.Flag("smartctl.device",
		"The device to monitor (repeatable)",
	).Strings()
	smartctlDeviceExclude = kingpin.Flag(
		"smartctl.device-exclude",
		"Regexp of devices to exclude from automatic scanning. (mutually exclusive to device-include)",
	).Default("").String()
	smartctlDeviceInclude = kingpin.Flag(
		"smartctl.device-include",
		"Regexp of devices to exclude from automatic scanning. (mutually exclusive to device-exclude)",
	).Default("").String()
	smartctlFakeData = kingpin.Flag("smartctl.fake-data",
		"The device to monitor (repeatable)",
	).Default("false").Hidden().Bool()
	ccissVolStatusPath = kingpin.Flag("ccissvolstatus.path",
		"The path to the cciss_vol_status binary",
	).Default("/usr/bin/cciss_vol_status").String()
)

// scanDevices uses smartctl to gather the list of available devices.
func scanDevices(logger log.Logger) []Device {
	filter := newDeviceFilter(*smartctlDeviceExclude, *smartctlDeviceInclude)

	baseDevices := readSMARTctlDevices(logger)
	raidDevices := readSMARTctlDevices(logger, "-d", "sat")

	scanDevices := []Device{}

	isExists := map[string]bool{}
	for _, d := range baseDevices.Get("devices").Array() {
		level.Debug(logger).Log("base_device: ", d)
		isExists[strings.TrimSpace(d.Get("info_name").String())] = true

		deviceName := getDiskName(
			strings.TrimSpace(d.Get("name").String()),
			strings.TrimSpace(d.Get("info_name").String()),
		)

		device := Device{
			Name:      d.Get("name").String(),
			Info_Name: deviceName,
			Type:      d.Get("type").String(),
		}
		scanDevices = append(scanDevices, device)
	}

	for _, d := range raidDevices.Get("devices").Array() {
		if isExists[strings.TrimSpace(d.Get("info_name").String())] {
			continue
		}
		level.Debug(logger).Log("raid_device: ", d)

		devices := formatDevices(logger, d)
		scanDevices = append(scanDevices, devices...)
	}

	scanDeviceResult := []Device{}
	for _, d := range scanDevices {
		if filter.ignored(d.Info_Name) {
			level.Info(logger).Log("msg", "Ignoring device", "name", d.Info_Name)
		} else {
			level.Info(logger).Log("msg", "Found device", "name", d.Info_Name)
			scanDeviceResult = append(scanDeviceResult, d)
		}
	}
	return scanDeviceResult
}

func filterDevices(logger log.Logger, devices []Device, filters []string) []Device {
	var filtered []Device
	for _, d := range devices {
		for _, filter := range filters {
			level.Debug(logger).Log("msg", "filterDevices", "device", d.Info_Name, "filter", filter)
			if strings.Contains(d.Info_Name, filter) {
				filtered = append(filtered, d)
				break
			}
		}
	}
	return filtered
}

func main() {
	metricsPath := kingpin.Flag(
		"web.telemetry-path", "Path under which to expose metrics",
	).Default("/metrics").String()
	toolkitFlags := webflag.AddFlags(kingpin.CommandLine, ":9633")

	promlogConfig := &promlog.Config{}
	flag.AddFlags(kingpin.CommandLine, promlogConfig)
	kingpin.Version(version.Print("smartctl_exporter"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()
	logger := promlog.New(promlogConfig)

	level.Info(logger).Log("msg", "Starting smartctl_exporter", "version", version.Info())
	level.Info(logger).Log("msg", "Build context", "build_context", version.BuildContext())

	var devices []Device
	devices = scanDevices(logger)
	level.Info(logger).Log("msg", "Number of devices found", "count", len(devices))
	if len(*smartctlDevices) > 0 {
		level.Info(logger).Log("msg", "Devices specified", "devices", strings.Join(*smartctlDevices, ", "))
		devices = filterDevices(logger, devices, *smartctlDevices)
		level.Info(logger).Log("msg", "Devices filtered", "count", len(devices))
	}

	collector := SMARTctlManagerCollector{
		Devices: devices,
		logger:  logger,
	}

	if *smartctlRescanInterval >= 1*time.Second {
		level.Info(logger).Log("msg", "Start background scan process")
		level.Info(logger).Log("msg", "Rescanning for devices every", "rescanInterval", *smartctlRescanInterval)
		go collector.RescanForDevices()
	}

	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewGoCollector(),
	)

	prometheus.WrapRegistererWithPrefix("", reg).MustRegister(&collector)

	http.Handle(*metricsPath, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	if *metricsPath != "/" && *metricsPath != "" {
		landingConfig := web.LandingConfig{
			Name:        "smartctl_exporter",
			Description: "Prometheus Exporter for S.M.A.R.T. devices",
			Version:     version.Info(),
			Links: []web.LandingLinks{
				{
					Address: *metricsPath,
					Text:    "Metrics",
				},
			},
		}
		landingPage, err := web.NewLandingPage(landingConfig)
		if err != nil {
			level.Error(logger).Log("err", err)
			os.Exit(1)
		}
		http.Handle("/", landingPage)
	}

	srv := &http.Server{}
	if err := web.ListenAndServe(srv, toolkitFlags, logger); err != nil {
		level.Error(logger).Log("err", err)
		os.Exit(1)
	}
}
