#!/usr/bin/env python3
"""Render docs/api/openapi.json as Markdown (docs/api/reference.md).

Stdlib-only, deterministic. Driven by script/gen-api-docs.sh; the OpenAPI
spec stays the source of truth (internal/server/v1_test.go pins it against
the handlers) — never hand-edit the rendered output.
"""
import json
import sys


def anchor(name):
    return name.lower().replace(" ", "-").replace("/", "").replace("{", "").replace("}", "")


def deref(spec, node):
    if isinstance(node, dict) and "$ref" in node:
        parts = node["$ref"].lstrip("#/").split("/")
        target = spec
        for p in parts:
            target = target[p]
        return target, parts[-1]
    return node, None


def schema_type(spec, schema):
    schema = schema or {}
    if "$ref" in schema:
        name = schema["$ref"].split("/")[-1]
        return f"[{name}](#{anchor(name)})"
    t = schema.get("type", "object")
    if t == "array":
        return f"array of {schema_type(spec, schema.get('items', {}))}"
    if schema.get("enum"):
        values = ", ".join(f"`{v}`" for v in schema["enum"])
        return f"{t} ({values})"
    if t == "object" and schema.get("properties"):
        fields = ", ".join(f"`{k}`: {schema_type(spec, v)}" for k, v in schema["properties"].items())
        return f"object {{{fields}}}"
    return t


def schema_table(spec, schema, out):
    schema, _ = deref(spec, schema)
    props = schema.get("properties")
    if not props:
        out.append(f"Type: {schema_type(spec, schema)}\n")
        return
    required = set(schema.get("required", []))
    out.append("| Field | Type | Required | Description |")
    out.append("| --- | --- | --- | --- |")
    for name, prop in props.items():
        desc = " ".join((prop.get("description", "")).split())
        req = "yes" if name in required else "no"
        out.append(f"| `{name}` | {schema_type(spec, prop)} | {req} | {desc} |")
    out.append("")


def render_parameters(spec, params, out):
    if not params:
        return
    out.append("**Parameters**\n")
    out.append("| Name | In | Type | Required | Description |")
    out.append("| --- | --- | --- | --- | --- |")
    for param in params:
        param, _ = deref(spec, param)
        desc = " ".join((param.get("description", "")).split())
        req = "yes" if param.get("required") else "no"
        ptype = schema_type(spec, param.get("schema", {}))
        out.append(f"| `{param['name']}` | {param['in']} | {ptype} | {req} | {desc} |")
    out.append("")


def render_body(spec, operation, out):
    body = operation.get("requestBody")
    if not body:
        return
    body, _ = deref(spec, body)
    content = body.get("content", {}).get("application/json", {})
    if not content:
        return
    out.append("**Request body** (`application/json`)\n")
    schema_table(spec, content.get("schema", {}), out)


def render_responses(spec, operation, out):
    out.append("**Responses**\n")
    out.append("| Status | Description | Body |")
    out.append("| --- | --- | --- |")
    for status, resp in operation.get("responses", {}).items():
        resp, ref_name = deref(spec, resp)
        desc = " ".join((resp.get("description", "")).split())
        schema = resp.get("content", {}).get("application/json", {}).get("schema")
        body = schema_type(spec, schema) if schema else (f"[{ref_name}]" if ref_name else "—")
        out.append(f"| {status} | {desc} | {body if schema else '—'} |")
    out.append("")


def render_operation(spec, path, method, operation, out):
    out.append(f"### {method.upper()} `{path}`\n")
    if operation.get("summary"):
        out.append(operation["summary"] + "\n")
    scope = operation.get("x-required-scope")
    if scope:
        out.append(f"Required scope: `{scope}`\n")
    if operation.get("description"):
        out.append(operation["description"] + "\n")
    render_parameters(spec, operation.get("parameters", []), out)
    render_body(spec, operation, out)
    render_responses(spec, operation, out)


def render(spec):
    info = spec["info"]
    out = [f"# {info['title']} — reference", ""]
    out.append(f"Version {info['version']} · OpenAPI {spec['openapi']} · "
               f"generated from `docs/api/openapi.json` by `script/gen-api-docs.sh` — do not edit by hand.")
    out.append("")
    out.append(info.get("description", ""))
    out.append("")
    for server in spec.get("servers", []):
        out.append(f"Server: `{server['url']}` — {server.get('description', '')}")
    out.append("")
    out.append("## Authentication\n")
    for name, scheme in spec.get("components", {}).get("securitySchemes", {}).items():
        out.append(f"**{name}** ({scheme.get('type')}, {scheme.get('scheme')}"
                   f"{', format `' + scheme['bearerFormat'] + '`' if scheme.get('bearerFormat') else ''}): "
                   f"{scheme.get('description', '')}\n")
    out.append("## Endpoints\n")
    for path, methods in spec["paths"].items():
        for method, operation in methods.items():
            render_operation(spec, path, method, operation, out)
    out.append("## Schemas\n")
    for name, schema in spec.get("components", {}).get("schemas", {}).items():
        out.append(f"### {name}\n")
        if schema.get("description"):
            out.append(schema["description"] + "\n")
        schema_table(spec, schema, out)
    return "\n".join(out).rstrip() + "\n"


def main():
    if len(sys.argv) != 2:
        sys.exit("usage: openapi_to_md.py <openapi.json>")
    with open(sys.argv[1]) as f:
        spec = json.load(f)
    sys.stdout.write(render(spec))


if __name__ == "__main__":
    main()
