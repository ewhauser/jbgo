# Eval Report: openai/gpt-5.4

- Mode: `bash`
- Timestamp: `2026-03-23T00:13:36Z`
- Max turns: `10`

## Summary

- Tasks passed: `47/58`
- Score: `275.0/315.0` (`87%`)
- Tool call success: `66/77` (`86%`)
- Tokens: `74590` input / `19102` output
- Duration: `276.8s`

## By Category

| Category | Passed | Tasks | Score |
|---|---:|---:|---:|
| archive_operations | 2 | 2 | 100% |
| build_simulation | 2 | 2 | 100% |
| code_search | 2 | 2 | 100% |
| complex_tasks | 5 | 6 | 81% |
| config_management | 2 | 2 | 100% |
| data_transformation | 5 | 6 | 86% |
| database_operations | 1 | 2 | 67% |
| environment | 2 | 2 | 100% |
| error_recovery | 2 | 2 | 100% |
| file_operations | 3 | 4 | 71% |
| json_processing | 6 | 8 | 95% |
| pipelines | 4 | 5 | 80% |
| scripting | 4 | 7 | 80% |
| system_info | 1 | 2 | 50% |
| text_processing | 6 | 6 | 100% |

## Task Results

| Task | Category | Status | Score | Turns | Calls |
|---|---|---|---:|---:|---:|
| file_ops_project_scaffold | file_operations | PASS | 7/7 | 2 | 1 |
| file_ops_backup_rename | file_operations | PASS | 5/5 | 2 | 1 |
| file_ops_find_and_delete | file_operations | PASS | 4/4 | 2 | 1 |
| text_log_error_count | text_processing | PASS | 2/2 | 2 | 1 |
| text_hostname_replace | text_processing | PASS | 4/4 | 2 | 1 |
| text_csv_revenue | text_processing | PASS | 2/2 | 2 | 1 |
| pipe_word_frequency | pipelines | PASS | 2/2 | 2 | 1 |
| pipe_log_pipeline | pipelines | PASS | 3/3 | 2 | 1 |
| script_fizzbuzz | scripting | PASS | 6/6 | 2 | 1 |
| script_array_stats | scripting | FAIL | 0/4 | 1 | 0 |
| script_function_lib | scripting | FAIL | 3/4 | 2 | 1 |
| data_csv_to_json | data_transformation | PASS | 5/5 | 2 | 1 |
| data_json_query | data_transformation | PASS | 4/4 | 2 | 1 |
| data_log_summarize | data_transformation | PASS | 7/7 | 3 | 2 |
| error_missing_file | error_recovery | PASS | 4/4 | 2 | 1 |
| error_graceful_parse | error_recovery | PASS | 2/2 | 2 | 1 |
| sysinfo_env_report | system_info | FAIL | 1/4 | 2 | 3 |
| sysinfo_date_calc | system_info | PASS | 2/2 | 5 | 4 |
| archive_create_extract | archive_operations | PASS | 2/2 | 2 | 1 |
| archive_selective | archive_operations | PASS | 4/4 | 2 | 1 |
| json_nested_names | json_processing | PASS | 5/5 | 2 | 1 |
| json_api_pagination | json_processing | FAIL | 4/5 | 2 | 1 |
| complex_todo_app | complex_tasks | PASS | 5/5 | 2 | 1 |
| complex_markdown_toc | complex_tasks | PASS | 4/4 | 2 | 1 |
| complex_diff_report | complex_tasks | PASS | 6/6 | 3 | 2 |
| json_config_merge | json_processing | PASS | 8/8 | 2 | 1 |
| json_ndjson_error_aggregate | json_processing | PASS | 7/7 | 2 | 1 |
| json_api_schema_migration | json_processing | PASS | 8/8 | 2 | 1 |
| json_to_csv_export | json_processing | FAIL | 7/9 | 2 | 1 |
| json_package_update | json_processing | PASS | 7/7 | 2 | 1 |
| json_order_totals | json_processing | PASS | 7/7 | 2 | 1 |
| pipe_dedup_merge | pipelines | PASS | 5/5 | 2 | 1 |
| text_multifile_replace | text_processing | PASS | 5/5 | 2 | 1 |
| script_health_check | scripting | PASS | 4/4 | 2 | 1 |
| data_column_transform | data_transformation | PASS | 6/6 | 2 | 1 |
| complex_release_notes | complex_tasks | PASS | 8/8 | 3 | 2 |
| data_csv_join | data_transformation | FAIL | 2/7 | 2 | 1 |
| search_recursive_grep | code_search | PASS | 7/7 | 5 | 4 |
| search_find_replace | code_search | PASS | 6/6 | 2 | 1 |
| config_env_defaults | environment | PASS | 7/7 | 2 | 1 |
| file_path_organizer | file_operations | FAIL | 1/8 | 2 | 1 |
| script_trap_cleanup | scripting | PASS | 5/5 | 2 | 1 |
| script_getopts_parser | scripting | FAIL | 3/5 | 4 | 3 |
| script_assoc_array | scripting | PASS | 7/7 | 3 | 2 |
| pipe_process_sub | pipelines | FAIL | 3/7 | 2 | 1 |
| pipe_xargs_batch | pipelines | PASS | 3/3 | 2 | 1 |
| text_heredoc_config | text_processing | PASS | 8/8 | 2 | 1 |
| text_comm_setops | text_processing | PASS | 7/7 | 2 | 1 |
| env_source_export | environment | PASS | 6/6 | 2 | 1 |
| complex_test_output | complex_tasks | FAIL | 3/10 | 2 | 1 |
| complex_debug_script | complex_tasks | PASS | 3/3 | 4 | 3 |
| data_regex_extract | data_transformation | PASS | 6/6 | 3 | 2 |
| db_csv_group_by | database_operations | PASS | 7/7 | 2 | 1 |
| db_csv_join_aggregate | database_operations | FAIL | 1/5 | 2 | 1 |
| config_env_template | config_management | PASS | 7/7 | 3 | 2 |
| config_ini_merge | config_management | PASS | 7/7 | 3 | 2 |
| build_multi_stage | build_simulation | PASS | 6/6 | 2 | 1 |
| build_script_generator | build_simulation | PASS | 5/5 | 3 | 2 |
