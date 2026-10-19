-- GitLab, Jira and Figma join GitHub.
ALTER TABLE integration_tokens DROP CONSTRAINT integration_tokens_provider_check;
ALTER TABLE integration_tokens ADD CONSTRAINT integration_tokens_provider_check CHECK (provider IN ('github', 'gitlab', 'jira', 'figma'));
