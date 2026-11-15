# pairing-code-format Specification

## Purpose
TBD - created by archiving change core-2132-pairing-code-alphabet. Update Purpose after archive.

## Requirements

### Requirement: Pairing codes avoid look-alike characters

The server SHALL mint pairing codes as 8 characters of Crockford base32, which contains no `I`, `L`,
`O` or `U`, from 40 random bits.

#### Scenario: Minted code

- **WHEN** an owner mints a pairing code
- **THEN** the code is 8 characters from `0123456789ABCDEFGHJKMNPQRSTVWXYZ`

### Requirement: Entered pairing codes tolerate look-alike readings

The server SHALL normalize an entered pairing code before redeeming it by ignoring case, spaces and
hyphens and folding `O` to `0` and `I` and `L` to `1`.

#### Scenario: Code typed with letters for digits

- **WHEN** a device enters a valid code with `O` in place of `0` or `l` in place of `1`, in lower case
  or with a hyphen
- **THEN** the code redeems as if typed exactly
