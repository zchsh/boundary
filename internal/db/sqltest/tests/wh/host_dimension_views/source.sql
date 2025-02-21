-- source tests the whx_host_dimension_source view.
begin;
  select plan(2);
  select wtt_load('widgets', 'iam', 'kms', 'auth', 'hosts', 'targets');

  -- Static hosts
  select is(s.*, row(
    'h_____wb__01', 'static host',         'None',                      'None', '1.big.widget',
    's___2wb-sths', 'static host set',     'Big Widget Static Set 2',   'None',
    'c___wb-sthcl', 'static host catalog', 'Big Widget Static Catalog', 'None',
    't_________wb', 'tcp target',          'Big Widget Target',         'None', 0,              28800, 1,
    'p____bwidget', 'Big Widget Factory',  'None',
    'o_____widget', 'Widget Inc',          'None'
  )::whx_host_dimension_source)
    from whx_host_dimension_source as s
   where s.host_id     = 'h_____wb__01'
     and s.host_set_id = 's___2wb-sths'
     and s.target_id   = 't_________wb';

  -- Plugin based hosts
  select is(s.*, row(
    'h_____wb__01-plgh',  'plugin host',         'None',                      'None', 'Unsupported',
    's___2wb-plghs',      'plugin host set',     'Big Widget Plugin Set 2',   'None',
    'c___wb-plghcl',      'plugin host catalog', 'Big Widget Plugin Catalog', 'None',
    't_________wb',       'tcp target',          'Big Widget Target',         'None', 0,              28800, 1,
    'p____bwidget',       'Big Widget Factory',  'None',
    'o_____widget',       'Widget Inc',          'None'
    )::whx_host_dimension_source)
  from whx_host_dimension_source as s
  where s.host_id     = 'h_____wb__01-plgh'
    and s.host_set_id = 's___2wb-plghs'
    and s.target_id   = 't_________wb';

  select * from finish();
rollback;

