package gateway

import (
	"context"
	"net/http"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// A sandbox's metrics export (api.MetricsExport) is pushed by its node,
// which measures it; the gateway's part is header_secrets. A header named
// there is filled from the tenant's secret of that name, so a collector's
// credential can be kept on the gateway, sealed, and never sit in the
// client. It reaches the node as a header value, which the node holds as it
// holds environment values: never returned, logged or recorded.
//
// The endpoint is left to the node to check: it is the node that dials it,
// and its operator's --metrics-export-allow-private that decides whether a
// private address may be reached.

// resolveExportSecrets moves me's header_secrets into its headers. An error
// names the secret, never its value.
func (g *Gateway) resolveExportSecrets(ctx context.Context, p Principal, me *api.MetricsExport) *createFail {
	if me == nil || len(me.HeaderSecrets) == 0 {
		return nil
	}
	if g.sealer == nil {
		return failWith(http.StatusNotImplemented, api.CodeUnsupported, ErrNoSecretsKey.Error())
	}
	headers := make(map[string]string, len(me.Headers)+len(me.HeaderSecrets))
	for k, v := range me.Headers {
		headers[http.CanonicalHeaderKey(k)] = v
	}
	for header, name := range me.HeaderSecrets {
		h := http.CanonicalHeaderKey(header)
		if _, dup := headers[h]; dup {
			return failWith(http.StatusBadRequest, api.CodeInvalidRequest, "metrics_export: "+h+" is in both headers and header_secrets")
		}
		if checkSecretName(name) != nil {
			return failWith(http.StatusBadRequest, api.CodeInvalidRequest, "metrics_export: header_secrets: "+name+" is not a secret's name")
		}
		sealed, ok, err := g.storeFor(p).SealedSecret(ctx, p.Tenant, name)
		if err != nil {
			g.logf("store: %v", err)
			return transientFail(http.StatusServiceUnavailable, api.CodeUnavailable, ErrStore.Error())
		}
		if !ok {
			return failWith(http.StatusBadRequest, api.CodeInvalidRequest, "metrics_export: secret "+name+" does not exist (sandbox-cli secret set "+name+")")
		}
		v, err := g.sealer.open(sealed, secretAD(p.Tenant, name))
		if err != nil {
			return failWith(http.StatusInternalServerError, api.CodeInternal, "secret "+name+" does not open with this gateway's secrets key; set it again")
		}
		headers[h] = string(v)
	}
	me.Headers, me.HeaderSecrets = headers, nil
	return nil
}
