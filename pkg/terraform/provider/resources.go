package provider

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceWorkflow() *schema.Resource {
	return &schema.Resource{
		Create: resourceWorkflowCreate,
		Read:   resourceWorkflowRead,
		Update: resourceWorkflowUpdate,
		Delete: resourceWorkflowDelete,
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
		Create: resourceWorkspaceCreate,
		Read:   resourceWorkspaceRead,
		Update: resourceWorkspaceUpdate,
		Delete: resourceWorkspaceDelete,
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
		Read: dataSourceWorkspaceRead,
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

// Implementations (stubs for build)
func resourceWorkflowCreate(d *schema.ResourceData, m any) error { return nil }
func resourceWorkflowRead(d *schema.ResourceData, m any) error   { return nil }
func resourceWorkflowUpdate(d *schema.ResourceData, m any) error { return nil }
func resourceWorkflowDelete(d *schema.ResourceData, m any) error { return nil }

func resourceWorkspaceCreate(d *schema.ResourceData, m any) error { return nil }
func resourceWorkspaceRead(d *schema.ResourceData, m any) error   { return nil }
func resourceWorkspaceUpdate(d *schema.ResourceData, m any) error { return nil }
func resourceWorkspaceDelete(d *schema.ResourceData, m any) error { return nil }

func dataSourceWorkspaceRead(d *schema.ResourceData, m any) error { return nil }
