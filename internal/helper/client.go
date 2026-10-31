package helper

import (
	"encoding/json"
	"net"
	"time"
)

func Call(sock string, r Request) (Response, error) {
	c, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		return Response{}, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(opTimeoutClient))
	if err := json.NewEncoder(c).Encode(r); err != nil {
		return Response{}, err
	}
	var resp Response
	err = json.NewDecoder(c).Decode(&resp)
	return resp, err
}

const opTimeoutClient = 46 * time.Minute
