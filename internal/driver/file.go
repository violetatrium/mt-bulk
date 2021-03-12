package driver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io/ioutil"
	"log"
	"path/filepath"
	"strings"

	"github.com/gocarina/gocsv"

	"gopkg.in/yaml.v2"

	"github.com/BurntSushi/toml"
	"github.com/migotom/mt-bulk/internal/entities"

	minim "github.com/MinimSecure/minim-api-examples/go"
)

func addToMinimInventory(cl *minim.Client, mac string, billingID string) {
	log.Println("attempting to setup", mac, "(billing ID: ", billingID, ")")
	resp, err := cl.PostJSON("/api/v1/isps/minim-prototypes/unums", minim.JSONObject{"id": mac})
	if err != nil {
		log.Println("failed to add to inventory:", err)
	} else {
		minim.CloseResponse(resp)
	}
	resp, err = cl.PostJSON("/api/v1/isps/minim-prototypes/unums/"+mac+"/enable", nil)
	if err != nil {
		log.Println("failed to enable:", err)
		return
	}
	data, err := minim.ParseJSONObject(resp)
	if err != nil {
		log.Println("failed to parse response:", err)
		return
	}
	mv, ok := data["id"].(string)
	if !ok {
		log.Println("response did not contain lan ID")
		return
	}
	resp, err = cl.PatchJSON("/api/v1/lans/"+mv, minim.JSONObject{"billing_integration_key": billingID})
	if err != nil {
		log.Println("failed to update billing ID:", err)
	} else {
		minim.CloseResponse(resp)
	}
	resp, err = cl.PostJSON("/api/v1/lans/"+mv+"/query_billing_service", nil)
	if err != nil {
		log.Println("failed to query billing service:", err)
	} else {
		minim.CloseResponse(resp)
	}
	log.Println("https://my.minim.co/lans/" + mv)
}

func MinimClientFromContext(ctx context.Context) (*minim.Client, error) {
	va, ok := ctx.Value("minim_app_id").(string)
	if !ok {
		return nil, errors.New("context did not contain minim_app_id")
	}
	vs, ok := ctx.Value("minim_secret").(string)
	if !ok {
		return nil, errors.New("context did not contain minim_secret")
	}
	return minim.New(va, vs), nil
}

// FileLoadJobs loads list of jobs from file.
func FileLoadJobs(ctx context.Context, jobTemplate entities.Job, filename string) (jobs []entities.Job, err error) {
	var hosts struct {
		Host []entities.Host
	}

	content, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	client, err := MinimClientFromContext(ctx)
	if err != nil {
		log.Println("unable to create minim API client:", err)
	}

	switch strings.ToLower(filepath.Ext(filename)) {
	case ".toml":
		err = toml.Unmarshal(content, &hosts)
		if err != nil {
			return nil, err
		}
	case ".yml", ".yaml":
		err = yaml.Unmarshal(content, &hosts)
		if err != nil {
			return nil, err
		}
	case ".csv":
		type H struct {
			BillingID string `csv:"BillingID"`
			Ether1MAC string `csv:"Ether1MAC"`
			IP        string `csv:"Addresses"`
			Type      string `csv:"Type"`
		}
		var hs []H
		err = gocsv.UnmarshalBytes(content, &hs)
		if err != nil {
			return nil, err
		}
		for _, h := range hs {
			// csv export from The Dude contains all Devices, so filter out everything that is not RouterOS
			if h.Type == "RouterOS" {
				hosts.Host = append(hosts.Host, entities.Host{IP: h.IP})
				if strings.Contains(h.Ether1MAC, ",") {
					p := strings.Split(strings.ReplaceAll(h.Ether1MAC, " ", ""), ",")
					h.Ether1MAC = p[0]
				}
				if client != nil {
					addToMinimInventory(client, h.Ether1MAC, h.BillingID)
				}
			}
		}
	default:
		reader := bytes.NewReader(content)
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			hosts.Host = append(hosts.Host, entities.Host{IP: scanner.Text()})
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}

	for _, host := range hosts.Host {
		job := jobTemplate
		job.Host = host
		if err := job.Host.Parse(); err != nil {
			log.Printf("Skipping host: %s\n", err)
			continue
		}

		jobs = append(jobs, job)
	}

	return jobs, nil
}
