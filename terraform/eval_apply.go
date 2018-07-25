package terraform

import (
	"fmt"
	"log"

	"github.com/hashicorp/go-multierror"

	"github.com/hashicorp/terraform/addrs"
	"github.com/hashicorp/terraform/configs"
	"github.com/hashicorp/terraform/plans"
	"github.com/hashicorp/terraform/states"
)

// EvalApply is an EvalNode implementation that writes the diff to
// the full diff.
type EvalApply struct {
	Addr      addrs.ResourceInstance
	State     **states.ResourceInstanceObject
	Change    **plans.ResourceInstanceChange
	Provider  *ResourceProvider
	Output    **states.ResourceInstanceObject
	CreateNew *bool
	Error     *error
}

// TODO: test
func (n *EvalApply) Eval(ctx EvalContext) (interface{}, error) {
	return nil, fmt.Errorf("EvalApply is not yet updated for the new state and plan types")
	/*
		diff := *n.Diff
		provider := *n.Provider
		state := *n.State

		// The provider API still expects our legacy InstanceInfo type, so we must shim it.
		legacyInfo := NewInstanceInfo(n.Addr.Absolute(ctx.Path()))

		if state == nil {
			state = &states.ResourceInstanceObject{}
		}

		// Flag if we're creating a new instance
		if n.CreateNew != nil {
			*n.CreateNew = state.ID == "" && !diff.GetDestroy() || diff.RequiresNew()
		}

		// With the completed diff, apply!
		log.Printf("[DEBUG] apply %s: executing Apply", n.Addr)
		state, err := provider.Apply(legacyInfo, state, diff)
		if state == nil {
			state = new(InstanceState)
		}
		state.init()

		// Force the "id" attribute to be our ID
		if state.ID != "" {
			state.Attributes["id"] = state.ID
		}

		// If the value is the unknown variable value, then it is an error.
		// In this case we record the error and remove it from the state
		for ak, av := range state.Attributes {
			if av == config.UnknownVariableValue {
				err = multierror.Append(err, fmt.Errorf(
					"Attribute with unknown value: %s", ak))
				delete(state.Attributes, ak)
			}
		}

		// If the provider produced an InstanceState with an empty id then
		// that really means that there's no state at all.
		// FIXME: Change the provider protocol so that the provider itself returns
		// a null in this case, and stop treating the ID as special.
		if state.ID == "" {
			state = nil
		}

		// Write the final state
		if n.Output != nil {
			*n.Output = state
		}

		// If there are no errors, then we append it to our output error
		// if we have one, otherwise we just output it.
		if err != nil {
			if n.Error != nil {
				helpfulErr := fmt.Errorf("%s: %s", n.Addr, err.Error())
				*n.Error = multierror.Append(*n.Error, helpfulErr)
			} else {
				return nil, err
			}
		}

		return nil, nil
	*/
}

// EvalApplyPre is an EvalNode implementation that does the pre-Apply work
type EvalApplyPre struct {
	Addr   addrs.ResourceInstance
	Gen    states.Generation
	State  **states.ResourceInstanceObject
	Change **plans.ResourceInstanceChange
}

// TODO: test
func (n *EvalApplyPre) Eval(ctx EvalContext) (interface{}, error) {
	change := *n.Change
	absAddr := n.Addr.Absolute(ctx.Path())

	if resourceHasUserVisibleApply(n.Addr) {
		priorState := change.Before
		plannedNewState := change.After

		err := ctx.Hook(func(h Hook) (HookAction, error) {
			return h.PreApply(absAddr, n.Gen, priorState, plannedNewState)
		})
		if err != nil {
			return nil, err
		}
	}

	return nil, nil
}

// EvalApplyPost is an EvalNode implementation that does the post-Apply work
type EvalApplyPost struct {
	Addr  addrs.ResourceInstance
	Gen   states.Generation
	State **states.ResourceInstanceObject
	Error *error
}

// TODO: test
func (n *EvalApplyPost) Eval(ctx EvalContext) (interface{}, error) {
	state := *n.State

	if resourceHasUserVisibleApply(n.Addr) {
		absAddr := n.Addr.Absolute(ctx.Path())
		newState := state.Value
		var err error
		if n.Error != nil {
			err = *n.Error
		}

		hookErr := ctx.Hook(func(h Hook) (HookAction, error) {
			return h.PostApply(absAddr, n.Gen, newState, err)
		})
		if hookErr != nil {
			return nil, hookErr
		}
	}

	return nil, *n.Error
}

// resourceHasUserVisibleApply returns true if the given resource is one where
// apply actions should be exposed to the user.
//
// Certain resources do apply actions only as an implementation detail, so
// these should not be advertised to code outside of this package.
func resourceHasUserVisibleApply(addr addrs.ResourceInstance) bool {
	// Only managed resources have user-visible apply actions.
	// In particular, this excludes data resources since we "apply" these
	// only as an implementation detail of removing them from state when
	// they are destroyed. (When reading, they don't get here at all because
	// we present them as "Refresh" actions.)
	return addr.ContainingResource().Mode == addrs.ManagedResourceMode
}

// EvalApplyProvisioners is an EvalNode implementation that executes
// the provisioners for a resource.
//
// TODO(mitchellh): This should probably be split up into a more fine-grained
// ApplyProvisioner (single) that is looped over.
type EvalApplyProvisioners struct {
	Addr           addrs.ResourceInstance
	State          **states.ResourceInstanceObject
	ResourceConfig *configs.Resource
	CreateNew      *bool
	Error          *error

	// When is the type of provisioner to run at this point
	When configs.ProvisionerWhen
}

// TODO: test
func (n *EvalApplyProvisioners) Eval(ctx EvalContext) (interface{}, error) {
	absAddr := n.Addr.Absolute(ctx.Path())
	state := *n.State
	if state == nil {
		log.Printf("[TRACE] EvalApplyProvisioners: %s has no state, so skipping provisioners", n.Addr)
		return nil, nil
	}

	if n.CreateNew != nil && !*n.CreateNew {
		// If we're not creating a new resource, then don't run provisioners
		return nil, nil
	}

	provs := n.filterProvisioners()
	if len(provs) == 0 {
		// We have no provisioners, so don't do anything
		return nil, nil
	}

	// taint tells us whether to enable tainting.
	taint := n.When == configs.ProvisionerWhenCreate

	if n.Error != nil && *n.Error != nil {
		if taint {
			state.Status = states.ObjectTainted
		}

		// We're already tainted, so just return out
		return nil, nil
	}

	{
		// Call pre hook
		err := ctx.Hook(func(h Hook) (HookAction, error) {
			return h.PreProvisionInstance(absAddr, state.Value)
		})
		if err != nil {
			return nil, err
		}
	}

	// If there are no errors, then we append it to our output error
	// if we have one, otherwise we just output it.
	err := n.apply(ctx, provs)
	if err != nil {
		if taint {
			state.Status = states.ObjectTainted
		}

		*n.Error = multierror.Append(*n.Error, err)
		return nil, err
	}

	{
		// Call post hook
		err := ctx.Hook(func(h Hook) (HookAction, error) {
			return h.PostProvisionInstance(absAddr, state.Value)
		})
		if err != nil {
			return nil, err
		}
	}

	return nil, nil
}

// filterProvisioners filters the provisioners on the resource to only
// the provisioners specified by the "when" option.
func (n *EvalApplyProvisioners) filterProvisioners() []*configs.Provisioner {
	// Fast path the zero case
	if n.ResourceConfig == nil || n.ResourceConfig.Managed == nil {
		return nil
	}

	if len(n.ResourceConfig.Managed.Provisioners) == 0 {
		return nil
	}

	result := make([]*configs.Provisioner, 0, len(n.ResourceConfig.Managed.Provisioners))
	for _, p := range n.ResourceConfig.Managed.Provisioners {
		if p.When == n.When {
			result = append(result, p)
		}
	}

	return result
}

func (n *EvalApplyProvisioners) apply(ctx EvalContext, provs []*configs.Provisioner) error {
	return fmt.Errorf("EvalApplyProvisioners.apply not yet updated for new types")
	/*
		instanceAddr := n.Addr
		absAddr := instanceAddr.Absolute(ctx.Path())
		state := *n.State

		// The hook API still uses the legacy InstanceInfo type, so we need to shim it.
		legacyInfo := NewInstanceInfo(n.Addr.Absolute(ctx.Path()))

		// Store the original connection info, restore later
		origConnInfo := state.Ephemeral.ConnInfo
		defer func() {
			state.Ephemeral.ConnInfo = origConnInfo
		}()

		var diags tfdiags.Diagnostics

		for _, prov := range provs {
			// Get the provisioner
			provisioner := ctx.Provisioner(prov.Type)
			schema := ctx.ProvisionerSchema(prov.Type)

			keyData := EvalDataForInstanceKey(instanceAddr.Key)

			// Evaluate the main provisioner configuration.
			config, _, configDiags := ctx.EvaluateBlock(prov.Config, schema, instanceAddr, keyData)
			diags = diags.Append(configDiags)

			// A provisioner may not have a connection block
			if prov.Connection != nil {
				connInfo, _, connInfoDiags := ctx.EvaluateBlock(prov.Connection.Config, connectionBlockSupersetSchema, instanceAddr, keyData)
				diags = diags.Append(connInfoDiags)

				if configDiags.HasErrors() || connInfoDiags.HasErrors() {
					continue
				}

				// Merge the connection information, and also lower everything to strings
				// for compatibility with the communicator API.
				overlay := make(map[string]string)
				if origConnInfo != nil {
					for k, v := range origConnInfo {
						overlay[k] = v
					}
				}
				for it := connInfo.ElementIterator(); it.Next(); {
					kv, vv := it.Element()
					var k, v string

					// there are no unset or null values in a connection block, and
					// everything needs to map to a string.
					if vv.IsNull() {
						continue
					}

					err := gocty.FromCtyValue(kv, &k)
					if err != nil {
						// Should never happen, because connectionBlockSupersetSchema requires all primitives
						panic(err)
					}
					err = gocty.FromCtyValue(vv, &v)
					if err != nil {
						// Should never happen, because connectionBlockSupersetSchema requires all primitives
						panic(err)
					}

					overlay[k] = v
				}

				state.Ephemeral.ConnInfo = overlay
			}

			{
				// Call pre hook
				err := ctx.Hook(func(h Hook) (HookAction, error) {
					return h.PreProvisionInstanceStep(absAddr, prov.Type)
				})
				if err != nil {
					return err
				}
			}

			// The output function
			outputFn := func(msg string) {
				ctx.Hook(func(h Hook) (HookAction, error) {
					h.ProvisionOutput(absAddr, prov.Type, msg)
					return HookActionContinue, nil
				})
			}

			// The provisioner API still uses our legacy ResourceConfig type, so
			// we need to shim it.
			legacyRC := NewResourceConfigShimmed(config, schema)

			// Invoke the Provisioner
			output := CallbackUIOutput{OutputFn: outputFn}
			applyErr := provisioner.Apply(&output, state, legacyRC)

			// Call post hook
			hookErr := ctx.Hook(func(h Hook) (HookAction, error) {
				return h.PostProvisionInstanceStep(absAddr, prov.Type, applyErr)
			})

			// Handle the error before we deal with the hook
			if applyErr != nil {
				// Determine failure behavior
				switch prov.OnFailure {
				case configs.ProvisionerOnFailureContinue:
					log.Printf("[INFO] apply %s [%s]: error during provision, but continuing as requested in configuration", n.Addr, prov.Type)
				case configs.ProvisionerOnFailureFail:
					return applyErr
				}
			}

			// Deal with the hook
			if hookErr != nil {
				return hookErr
			}
		}

		return diags.ErrWithWarnings()
	*/
}
