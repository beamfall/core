## ADDED Requirements

### Requirement: A scraper may describe its credential

A scraper MAY declare credential help: a label for its AuthRef credential, an https link to where
the owner obtains it, and the link's label. The Console and setup SHALL show that label for the
credential field and the link beside it, and SHALL show the default key presentation when the
scraper declares none. Credential help SHALL never carry or reveal a stored secret.

#### Scenario: Subscriber PIN

- **WHEN** a scraper declares the label "Subscriber PIN" and a subscribe link
- **THEN** the provider-key field is labelled "Subscriber PIN" and the subscribe link opens in a new
  tab

#### Scenario: Unsafe link

- **WHEN** the declared help link is not an https URL
- **THEN** the label is shown and no link is rendered

#### Scenario: Saved PIN stays secret

- **WHEN** the owner saves a PIN and the Console reads its configuration again
- **THEN** the credential reports configured and the PIN value does not appear in the response
