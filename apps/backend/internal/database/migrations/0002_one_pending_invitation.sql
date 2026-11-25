-- Two invitations created at the same time could both stay pending. A pending invitation is one
-- that is not accepted yet. An owner invitation takes the owner slot of its organization, and a
-- client invitation takes its client.
CREATE UNIQUE INDEX invitations_one_pending_owner_per_org ON invitations (organization_id)
    WHERE role = 'owner' AND accepted_at IS NULL;
CREATE UNIQUE INDEX invitations_one_pending_per_client ON invitations (client_id)
    WHERE role = 'client' AND accepted_at IS NULL;
