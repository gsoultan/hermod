package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func Provider() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"endpoint": {
				Type:        schema.TypeString,
				Required:    true,
				DefaultFunc: schema.EnvDefaultFunc("HERMOD_ENDPOINT", nil),
			},
			"token": {
				Type:        schema.TypeString,
				Optional:    true,
				Sensitive:   true,
				DefaultFunc: schema.EnvDefaultFunc("HERMOD_TOKEN", nil),
			},
		},
		ResourcesMap: map[string]*schema.Resource{
			"hermod_workflow":  resourceWorkflow(),
			"hermod_workspace": resourceWorkspace(),
		},
		DataSourcesMap: map[string]*schema.Resource{
			"hermod_workspace": dataSourceWorkspace(),
		},
		// ConfigureContextFunc, not ConfigureFunc: the bare form is deprecated
		// because it cannot carry a cancellation.
		ConfigureContextFunc: providerConfigure,
	}
}

func providerConfigure(_ context.Context, d *schema.ResourceData) (any, diag.Diagnostics) {
	config := Config{
		Endpoint: d.Get("endpoint").(string),
		Token:    d.Get("token").(string),
	}
	return &config, nil
}

type Config struct {
	Endpoint string
	Token    string
}
