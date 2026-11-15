# Unambiguous pairing codes

Ticket: CORE-2132 (`queue:core:main`).

## Why

Pairing codes were minted in RFC 4648 base32 (`A`–`Z`, `2`–`7`). A code read off a TV or a phone
could contain `O`, `I` or `L`, which people read as `0` and `1`, so an owner could type a valid code
wrongly and be refused.

## What Changes

- Codes are minted in Crockford base32 (`0`–`9` and capitals without `I`, `L`, `O`, `U`). They stay
  8 characters from 5 random bytes, so entropy is unchanged at 40 bits.
- An entered code is normalized before lookup: surrounding whitespace, inner spaces and hyphens are
  dropped, letters are upper-cased, and `O` folds to `0`, `I` and `L` fold to `1`. Either reading of
  a code redeems it. The App Review demo enrollment key is normalized the same way.
- Server only. No client restricts which characters can be typed into the pairing field.

## Consequence

A code minted before the upgrade that contains `O`, `I` or `L` stops redeeming after the server
restarts. Codes expire after 10 minutes, so only codes minted just before the restart are affected.
`Z`/`2`, `S`/`5` and `B`/`8` can still look alike; removing them would need longer codes.

## Non-goals

- Changing the code length, expiry, roles or the enrollment flow.
