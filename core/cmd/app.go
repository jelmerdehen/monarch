package cmd

import (
	//"fmt"

	"github.com/urfave/cli/v2"
)

func NewApp(client *Client) *cli.App {
	app := cli.App{
		Usage:   "CLI for Monarch",
		Version: "v0.0.1-alpha",
		Commands: []*cli.Command{
			{
				Name:   "v4l2",
				Usage:  "start video4linux service",
				Action: client.V4l2,
			},
			{
				Name:   "x11grab",
				Usage:  "start x11grab service",
				Action: client.X11grab,
			},
			{
				Name:   "arecord",
				Usage:  "start arecord service",
				Action: client.Arecord,
			},
			{
				Name:   "air",
				Usage:  "start air service",
				Action: client.Air,
			},
			{
				Name:   "xidle",
				Usage:  "test xidle",
				Action: client.Xidle,
			},
			{
				Name:   "compress",
				Usage:  "Compress output",
				Action: client.Compress,
			},
		},
	}
	return &app
}
