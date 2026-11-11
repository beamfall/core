## 1. Implementation

- [x] 1.1 Label the Select triggers on Operations, Unmatched and Subscriptions (CORE-0089)
- [x] 1.2 Add the `scheduleUnit` string in all locales and update the strings pins
- [x] 1.3 Render one `h1` in the Manage shell, player page and browse page
- [x] 1.4 Make Manage section titles level-2 headings

- [x] 1.5 Label the Console Select triggers (access, remote, plugins) (CORE-0089)
- [x] 1.6 Render one `h1` in the Console shell ahead of the admin gate (CORE-0089)

## 2. Verification

- [x] 2.1 Playwright a11y spec for `button-name` and single `h1`
- [x] 2.2 `npm run check`, `npm run typecheck`, vitest, `openspec validate --all --strict`
- [x] 2.3 Extend the e2e to every Console route and viewer `/watch`, `/seeall`, `/profiles`
