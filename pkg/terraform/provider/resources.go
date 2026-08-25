package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// The Hermod Terraform provider is a declared schema without an
// implementation.
//
// Every CRUD function here was a stub returning nil — and returning nil is how
// this SDK says "that worked". A `terraform apply` against it reported every
// resource created, recorded them in state, and created nothing: the plan and
// the world disagreed permanently, and the next plan showed no drift because
// state said the work was done. Silently succeeding is the worst available
// answer for an operation that did not happen.
//
// Until the API calls exist these refuse instead, with an error naming what is
// missing. A provider that cannot do the thing is a nuisance; a provider that
// claims to have done it is a data-integrity problem.
//
// The Context variants are used throughout, not the deprecated bare ones: the
// SDK deprecated those because they cannot carry a cancellation, so a
// practitioner pressing Ctrl-C could not stop the work.

// errNotImplemented is the single place the refusal is worded.
func errNotImplemented(op, resource string) diag.Diagnostics {
	return diag.Diagnostics{{
		Severity: diag.Error,
		Summary:  "hermod provider: " + op + " is not implemented for " + resource,
		Detail: "This provider declares its schema but has no API implementation yet, " +
			"so it cannot create, read, update or delete anything in Hermod. It refuses " +
			"rather than reporting success for work it did not do — a plan that appeared " +
			"to apply would leave Terraform state and the Hermod deployment permanently " +
			"disagreeing, with no drift reported. Manage these resources through the " +
			"Hermod API or UI until the provider is implemented.",
	}}
}

func resourceWorkflow() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceWorkflowCreate,
		ReadContext:   resourceWorkflowRead,
		UpdateContext: resourceWorkflowUpdate,
		DeleteContext: resourceWorkflowDelete,
		Schema: map[string]*schema.Schema{
			"name": {
				Type:     schema.TypeString,
				Required: true,
			},
			"vhost": {
				Type:     schema.TypeString,
				Optional: true,
				Default:  "/",
			},
			"active": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  true,
			},
			"workspace_id": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"cpu_request": {
				Type:     schema.TypeFloat,
				Optional: true,
			},
			"memory_request": {
				Type:     schema.TypeFloat,
				Optional: true,
			},
			"throughput_request": {
				Type:     schema.TypeInt,
				Optional: true,
			},
		},
	}
}

func resourceWorkspace() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceWorkspaceCreate,
		ReadContext:   resourceWorkspaceRead,
		UpdateContext: resourceWorkspaceUpdate,
		DeleteContext: resourceWorkspaceDelete,
		Schema: map[string]*schema.Schema{
			"name": {
				Type:     schema.TypeString,
				Required: true,
			},
			"description": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"max_workflows": {
				Type:     schema.TypeInt,
				Optional: true,
			},
			"max_cpu": {
				Type:     schema.TypeFloat,
				Optional: true,
			},
			"max_memory": {
				Type:     schema.TypeFloat,
				Optional: true,
			},
			"max_throughput": {
				Type:     schema.TypeInt,
				Optional: true,
			},
		},
	}
}

func dataSourceWorkspace() *schema.Resource {
	return &schema.Resource{
		ReadContext: dataSourceWorkspaceRead,
		Schema: map[string]*schema.Schema{
			"name": {
				Type:     schema.TypeString,
				Required: true,
			},
			"id": {
				Type:     schema.TypeString,
				Computed: true,
			},
		},
	}
}

func resourceWorkflowCreate(_ context.Context, _ *schema.ResourceData, _ any) diag.Diagnostics {
	return errNotImplemented("create", "hermod_workflow")
}

func resourceWorkflowRead(_ context.Context, _ *schema.ResourceData, _ any) diag.Diagnostics {
	return errNotImplemented("read", "hermod_workflow")
}

func resourceWorkflowUpdate(_ context.Context, _ *schema.ResourceData, _ any) diag.Diagnostics {
	return errNotImplemented("update", "hermod_workflow")
}

func resourceWorkflowDelete(_ context.Context, _ *schema.ResourceData, _ any) diag.Diagnostics {
	return errNotImplemented("delete", "hermod_workflow")
}

func resourceWorkspaceCreate(_ context.Context, _ *schema.ResourceData, _ any) diag.Diagnostics {
	return errNotImplemented("create", "hermod_workspace")
}

func resourceWorkspaceRead(_ context.Context, _ *schema.ResourceData, _ any) diag.Diagnostics {
	return errNotImplemented("read", "hermod_workspace")
}

func resourceWorkspaceUpdate(_ context.Context, _ *schema.ResourceData, _ any) diag.Diagnostics {
	return errNotImplemented("update", "hermod_workspace")
}

func resourceWorkspaceDelete(_ context.Context, _ *schema.ResourceData, _ any) diag.Diagnostics {
	return errNotImplemented("delete", "hermod_workspace")
}

func dataSourceWorkspaceRead(_ context.Context, _ *schema.ResourceData, _ any) diag.Diagnostics {
	return errNotImplemented("read", "hermod_workspace data source")
}
