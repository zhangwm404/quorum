package testnode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	guuid "github.com/google/uuid"
	"github.com/rumsystem/quorum/internal/pkg/utils"
	quorumpb "github.com/rumsystem/rumchaindata/pkg/pb"
)

func RequestAPI(apiurl string, endpoint string, method string, data string) (int, []byte, error) {
	upperMethod := strings.ToUpper(method)
	methods := map[string]string{
		"HEAD":    http.MethodHead,
		"GET":     http.MethodGet,
		"POST":    http.MethodPost,
		"PUT":     http.MethodPut,
		"DELETE":  http.MethodDelete,
		"PATCH":   http.MethodPatch,
		"OPTIONS": http.MethodOptions,
	}

	if _, found := methods[upperMethod]; !found {
		panic(fmt.Sprintf("not support http method: %s", method))
	}

	method = methods[upperMethod]

	url := fmt.Sprintf("%s%s", apiurl, endpoint)
	if len(data) > 0 {
		log.Printf("request %s %s with body: %s", method, url, data)
	} else {
		log.Printf("request %s %s", method, url)

	}
	client, err := utils.NewHTTPClient()
	if err != nil {
		return 0, []byte(""), err
	}

	req, err := http.NewRequest(method, url, bytes.NewBufferString(data))
	if err != nil {
		return 0, []byte(""), err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := client.Do(req)
	if err != nil {
		return 0, []byte(""), err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, []byte(""), err
	}
	log.Printf("response status: %d body: %s", resp.StatusCode, body)
	return resp.StatusCode, body, nil
}

func CheckNodeRunning(ctx context.Context, url string) (string, bool) {
	apiurl := fmt.Sprintf("%s/api/v1", url)
	fmt.Printf("checkNodeRunning: %s\n", apiurl)
	ticker := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case <-ctx.Done():
			ticker.Stop()
			return "", false
		case <-ticker.C:
			_, resp, err := RequestAPI(apiurl, "/node", "GET", "")
			if err == nil {
				var objmap map[string]interface{}
				if err := json.Unmarshal(resp, &objmap); err != nil {
					fmt.Println(err)
				} else {
					if objmap["node_status"] == "NODE_ONLINE" && objmap["node_type"] == "bootstrap" {
						ticker.Stop()
						return objmap["node_id"].(string), true
					} else if objmap["peers"] != nil {
						for key, peers := range objmap["peers"].(map[string]interface{}) {
							reqpeers := []string{}
							if strings.Index(key, "/quorum/meshsub/") >= 0 {
								for _, p := range peers.([]interface{}) {
									reqpeers = append(reqpeers, p.(string))
								}
							}
							if len(reqpeers) >= 0 {
								ticker.Stop()
								return objmap["node_id"].(string), true
							}
						}
					}
				}
			}
		}
	}
}

func CheckApiServerRunning(ctx context.Context, baseUrl string) bool {
	ticker := time.NewTicker(100 * time.Millisecond)
	for {
		select {
		case <-ctx.Done():
			ticker.Stop()
			return false
		case <-ticker.C:
			statusCode, resp, err := RequestAPI(baseUrl, "/api/v1/node", "GET", "")
			if err != nil {
				fmt.Println(err)
				continue
			}
			if statusCode == 200 {
				var objmap map[string]interface{}
				if err := json.Unmarshal(resp, &objmap); err != nil {
					fmt.Println(err)
				}

				ticker.Stop()
				return true
			}
		}
	}
}

func GetAllGroupTrxIds(ctx context.Context, baseUrl string, group_id string, height_blockid string) *[]string {
	trxids := []string{}
	_, resp, err := RequestAPI(baseUrl, fmt.Sprintf("/api/v1/block/%s/%s", group_id, height_blockid), "GET", "")
	if err != nil {
		return &trxids
	}
	block := &quorumpb.Block{}
	if err := json.Unmarshal(resp, &block); err == nil {
		prevEpoch := block.Epoch - 1
		for {
			_, resp, err := RequestAPI(baseUrl, fmt.Sprintf("/api/v1/block/%s/%d", group_id, prevEpoch), "GET", "")
			if err != nil {
				break
			}
			err = json.Unmarshal(resp, &block)
			if err != nil {
				break
			}
			if prevEpoch == 0 || prevEpoch == block.Epoch-1 {
				break
			}

			for _, trx := range block.Trxs {
				trxids = append(trxids, trx.TrxId)
			}
			prevEpoch = block.Epoch - 1
		}

	}

	return &trxids
}

func SeedUrlToGroupId(seedurl string) string {
	if !strings.HasPrefix(seedurl, "rum://seed?") {
		return ""
	}
	u, err := url.Parse(seedurl)
	if err != nil {
		return ""
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return ""
	}
	b64gstr := q.Get("g")

	b64gbyte, err := base64.RawURLEncoding.DecodeString(b64gstr)
	b64guuid, err := guuid.FromBytes(b64gbyte)
	return b64guuid.String()
}
