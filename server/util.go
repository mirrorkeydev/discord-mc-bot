package server

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"strings"
	"time"

	pb "github.com/mirrorkeydev/discord-mc-bot/proto"
	log "github.com/sirupsen/logrus"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Waits for a GCP compute operation to complete.
// Referenced from https://github.com/googleapis/google-cloud-go/issues/178#issuecomment-489024603
func waitForOperation(op *compute.Operation) error {
	for {
		result, err := gcpComputeService.ZoneOperations.Get(gcpProjectId, gcpZone, op.Name).Do()
		if err != nil {
			return fmt.Errorf("failed retriving operation status: %s", err)
		}

		if result.Status == "DONE" {
			if result.Error != nil {
				var errors []string
				for _, e := range result.Error.Errors {
					errors = append(errors, e.Message)
				}
				return fmt.Errorf("operation failed with error(s): %s", strings.Join(errors, ", "))
			}
			break
		}
		time.Sleep(time.Second)
	}
	return nil
}

// Checks if the MC server is currently up, as reported by GCP compute.
func IsUp() (bool, error) {
	instance, err := gcpComputeService.Instances.Get(gcpProjectId, gcpZone, gcpServerName).Do()

	if err != nil {
		if e, ok := err.(*googleapi.Error); ok {
			if e.Code == 404 {
				return false, nil
			} else {
				log.WithError(err).Error("cannot get available instances")
				return false, err
			}
		} else {
			log.WithError(err).Error("cannot get available instances")
			return false, err
		}
	}

	switch instance.Status {
	case "RUNNING":
		return true, nil
	default:
		return false, nil
	}
}

// This connection will fail if the management server (which is hosted on the
// same instance as the MC server) isn't up yet. Therefore, this should only
// be called after somebody manually tells the bot to bring the server up.
func initiateConnectionToManagementServer() error {
	certificate, err := tls.LoadX509KeyPair(
		"certs/discord-mc-client.crt",
		"certs/discord-mc-client.key",
	)
	if err != nil {
		log.WithError(err).Error("failed to read ca cert files")
		return err
	}

	certPool := x509.NewCertPool()
	bs, err := ioutil.ReadFile("certs/discord-mc.crt")
	if err != nil {
		log.WithError(err).Error("failed to read ca cert")
		return err
	}

	ok := certPool.AppendCertsFromPEM(bs)
	if !ok {
		log.WithError(err).Error("failed to append certs")
		return err
	}

	transportCreds := credentials.NewTLS(&tls.Config{
		ServerName:   ManagementServerAddress,
		Certificates: []tls.Certificate{certificate},
		RootCAs:      certPool,
	})

	managementServerConnection, err = grpc.Dial(fmt.Sprintf("%v:%v", ManagementServerAddress, managementServerPort), grpc.WithBlock(), grpc.WithTransportCredentials(transportCreds))
	if err != nil {
		log.WithError(err).Error("did not connect: %v /n")
		return err
	}

	managementServerClient = pb.NewMCManagementClient(managementServerConnection)
	log.WithError(err).Error("Connected to MC management server!")
	return nil
}
