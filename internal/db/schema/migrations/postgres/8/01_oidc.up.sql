begin;

-- Invalidate auth tokens when the oidc auth tokens mutate in certain ways
create or replace function
    delete_auth_tokens_for_auth_method(a_method_id wt_public_id) returns void as $$
begin
    delete from auth_token
    where auth_account_id in
          (select public_id
           from auth_account
           where auth_method_id = a_method_id);
end;
$$ language plpgsql;

create or replace function
    invalidate_oidc_auth_tokens_on_auth_method_update()
    returns trigger
as $$
begin
    if new.issuer is distinct from old.issuer then
        execute delete_auth_tokens_for_auth_method(new.public_id);
    end if;
    if new.client_id is distinct from old.client_id then
        execute delete_auth_tokens_for_auth_method(new.public_id);
    end if;
    return new;
end;
$$ language plpgsql;


create trigger
    invalidate_oidc_auth_tokens_on_auth_method_update
before
update on auth_oidc_method
  for each row execute procedure invalidate_oidc_auth_tokens_on_auth_method_update();
  
commit;
