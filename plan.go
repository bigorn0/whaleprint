package main

import (
	"fmt"
	"io"
	"net/url"
	"os"

	"golang.org/x/net/context"

	"github.com/docker/docker/api/client/bundlefile"
	"github.com/docker/engine-api/client"
	"github.com/docker/engine-api/types"
	"github.com/docker/engine-api/types/swarm"
	"github.com/fatih/color"
	"github.com/urfave/cli"
)

var Replica1 uint64 = 1

type Services map[string]swarm.Service

func plan(c *cli.Context) error {
	stackName := c.Args().Get(0)

	if stackName == "" {
		return cli.NewExitError("Need to specify a stack name", 1)
	}
	dabLocation := c.String("dab")

	if dabLocation == "" {
		// Assume it is called as the stack name
		dabLocation = fmt.Sprintf("%s.dab", stackName)
	}

	var dabReader io.Reader
	if u, e := url.Parse(dabLocation); e == nil && u.IsAbs() {
		// DAB file seems to be remote, try to download it first
		return cli.NewExitError("Not implemented", 2)
	} else {
		if dabFile, err := os.Open(dabLocation); err != nil {
			return cli.NewExitError(err.Error(), 3)
		} else {
			dabReader = dabFile
		}
	}

	bundle, bundleErr := bundlefile.LoadFile(dabReader)
	if bundleErr != nil {
		return cli.NewExitError(bundleErr.Error(), 3)
	}

	swarm, swarmErr := client.NewEnvClient()
	if swarmErr != nil {
		return cli.NewExitError(swarmErr.Error(), 3)
	}

	services, servicesErr := swarm.ServiceList(context.Background(), types.ServiceListOptions{})
	if servicesErr != nil {
		return cli.NewExitError(servicesErr.Error(), 3)
	}

	expected := getBundleServicesSpec(bundle, stackName)
	current := getSwarmServicesSpecForStack(services, stackName)

	for n, es := range expected {
		if cs, found := current[n]; !found {
			color.Green("\n + %s << service will be added", n)
			// New service to add
		} else {
			color.Cyan("\n%s\n", es.Spec.Name)
			PrintServiceSpecDiff(cs.Spec, es.Spec)
		}
	}

	// Checks services to remove

	for n, _ := range current {
		if _, found := expected[n]; !found {
			color.Red("\n - %s << service will be removed", n)
		}
	}

	/*
		max := math.Max(len(current), len(expected))
		for i := 0; i < max; i++ {
			if i >= len(current) {
				// New service to add
			} else if i >= len(expected) {
				// Service to remove
			} else if current[i] != expected[i] {
				// Service to remove and service to add
			} else {
			}
		}*/

	return nil
}

func safeDereference(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func getBundleServicesSpec(bundle *bundlefile.Bundlefile, stack string) Services {
	specs := Services{}

	for name, service := range bundle.Services {
		spec := swarm.ServiceSpec{
			TaskTemplate: swarm.TaskSpec{
				ContainerSpec: swarm.ContainerSpec{
					Image:   service.Image,
					Labels:  service.Labels,
					Command: service.Command,
					Args:    service.Args,
					Env:     service.Env,
					Dir:     safeDereference(service.WorkingDir),
					User:    safeDereference(service.User),
				},
			},
			Mode: swarm.ServiceMode{
				Replicated: &swarm.ReplicatedService{
					Replicas: &Replica1,
				},
			},
		}
		spec.Labels = map[string]string{"com.docker.stack.namespace": stack}
		spec.Name = fmt.Sprintf("%s_%s", stack, name)

		// Populate ports
		ports := []swarm.PortConfig{}
		for _, port := range service.Ports {
			p := swarm.PortConfig{
				TargetPort: port.Port,
				Protocol:   swarm.PortConfigProtocol(port.Protocol),
			}

			ports = append(ports, p)
		}
		if len(ports) > 0 {
			spec.EndpointSpec = &swarm.EndpointSpec{Ports: ports}
		}

		service := swarm.Service{}
		service.ID = spec.Name
		service.Spec = spec

		specs[spec.Name] = service
	}
	return specs
}

func getSwarmServicesSpecForStack(services []swarm.Service, stack string) Services {
	specs := Services{}

	for _, service := range services {
		if service.Spec.Labels["com.docker.stack.namespace"] == stack {
			specs[service.Spec.Name] = service
		}
	}

	return specs
}
