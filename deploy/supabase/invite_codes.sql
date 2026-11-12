-- Plexodus invite code directory: maps a short invite code to the address
-- of the Plexodus server that issued it, so a friend can type the code into
-- the app without knowing the server's address. Only these three functions
-- are reachable with the public key; the table itself is not exposed.

create schema if not exists plexodus;
revoke all on schema plexodus from public, anon, authenticated;

create table if not exists plexodus.invite_codes (
    code        text primary key check (code ~ '^[A-Z2-9]{8}$'),
    server_url  text not null check (server_url ~ '^https?://' and length(server_url) <= 300),
    secret_hash text not null,
    expires_at  timestamptz not null,
    created_at  timestamptz not null default now()
);
alter table plexodus.invite_codes enable row level security;

-- register (or re-register) a code. Re-registering needs the same secret.
create or replace function public.plexodus_register(p_code text, p_url text, p_secret text, p_expires timestamptz)
returns boolean language plpgsql security definer set search_path = '' as $$
declare h text := encode(extensions.digest(p_secret, 'sha256'), 'hex');
begin
  if p_secret is null or length(p_secret) < 16 or length(p_secret) > 200 then
    raise exception 'bad secret';
  end if;
  if p_expires > now() + interval '400 days' then
    p_expires := now() + interval '400 days';
  end if;
  delete from plexodus.invite_codes where expires_at < now();
  insert into plexodus.invite_codes(code, server_url, secret_hash, expires_at)
    values (upper(p_code), p_url, h, p_expires)
    on conflict (code) do update set server_url = excluded.server_url, expires_at = excluded.expires_at
    where plexodus.invite_codes.secret_hash = h;
  return found;
end $$;

-- resolve a code to its server address, or null.
create or replace function public.plexodus_resolve(p_code text)
returns text language sql stable security definer set search_path = '' as $$
  select server_url from plexodus.invite_codes
  where code = upper(regexp_replace(coalesce(p_code, ''), '[^A-Za-z0-9]', '', 'g')) and expires_at > now();
$$;

-- revoke a code (the issuing server proves it with its secret).
create or replace function public.plexodus_revoke(p_code text, p_secret text)
returns boolean language plpgsql security definer set search_path = '' as $$
begin
  delete from plexodus.invite_codes
    where code = upper(p_code) and secret_hash = encode(extensions.digest(p_secret, 'sha256'), 'hex');
  return found;
end $$;

revoke all on function public.plexodus_register(text, text, text, timestamptz) from public;
revoke all on function public.plexodus_resolve(text) from public;
revoke all on function public.plexodus_revoke(text, text) from public;
grant execute on function public.plexodus_register(text, text, text, timestamptz) to anon, authenticated;
grant execute on function public.plexodus_resolve(text) to anon, authenticated;
grant execute on function public.plexodus_revoke(text, text) to anon, authenticated;
