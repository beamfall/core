## MODIFIED Requirements

### Requirement: The GraphQL surface describes itself

The `agentCatalog` query SHALL list every top-level GraphQL field with its name, kind (`query` or
`mutation`), whether it requires an Admin Session, the token tiers that may run it, and its
arguments, and SHALL include a short guide for agents. The list SHALL be derived from the dispatcher
registries. Each field's arguments SHALL come from an argument registry that names every argument
the field's resolvers read, with its kind and whether it is optional, and every field SHALL report
`argumentsKnown` as true. A conformance test SHALL fail when a resolver reads an argument the
registry does not list for that field. An Admin Session and a token of any tier SHALL be able to
read it.

#### Scenario: Agent discovers what it may run

- **WHEN** a `diagnostics` token queries `agentCatalog`
- **THEN** `jobs` is listed as a query requiring an Admin Session with tiers `diagnostics` and
  `agent`, and `createApiToken` is listed with no tiers

#### Scenario: Agent reads a field's arguments

- **WHEN** an agent queries `agentCatalog`
- **THEN** `job` reports `argumentsKnown` true with a required `id` string argument, and a field
  that takes no arguments reports `argumentsKnown` true with an empty argument list

#### Scenario: Resolver reads an unlisted argument

- **WHEN** a resolver reads a request argument that the registry does not list for its field
- **THEN** the conformance test fails naming the field and the argument
