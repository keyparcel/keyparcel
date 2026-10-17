#pragma once
#include <gtk/gtk.h>
#include <stdint.h>

typedef struct UI UI;
UI *ui_new(uintptr_t handle, const char *icon);
int ui_run(UI *ui);
void ui_ref(UI *ui);
void ui_unref(UI *ui);
int ui_pending(UI *ui);
void ui_iterate(void);
void ui_post(UI *ui, uintptr_t callback);
void ui_shutdown(UI *ui);
void ui_page(UI *ui, const char *page);
void ui_message(UI *ui, const char *text);
void ui_confirmation(UI *ui, const char *text);
void ui_progress(UI *ui, const char *text);
void ui_busy(UI *ui, int busy);
const char *ui_get(UI *ui, const char *name);
void ui_set(UI *ui, const char *name, const char *text);
char *ui_draft(UI *ui);
char *ui_selection(UI *ui);
void ui_clear(UI *ui, const char *name);
void ui_item(UI *ui, const char *id, const char *title, const char *detail, int can_view);
void ui_device(UI *ui, const char *fp, const char *detail, int supported, int trusted, int self);
void ui_host(UI *ui, const char *name);
void ui_view(UI *ui, const char *text, const char *notice, int eligible);
void ui_saved_origin(UI *ui, int parcel);
void ui_clear_results(UI *ui);
void ui_picker(UI *ui, int save, const char *filename);
void ui_confirm(UI *ui, const char *action, const char *message);
void ui_approve(UI *ui, const char *fp, const char *message, const char *name);
int ui_copy(UI *ui, const char *text);
int ui_askpass(const char *prompt);
void ui_cancel_picker(UI *ui);
int ui_picker_pending(UI *ui);

void ui_details(UI *ui, const char *id);
void ui_send_ready(UI *ui, int has_file);
void ui_clear_draft(UI *ui, int file);

void ui_connection(UI *ui, const char *state);
void ui_refreshing(UI *ui, int refreshing);

void ui_rows_begin(UI *ui, const char *name);
void ui_rows_end(UI *ui);

void ui_watch(UI *ui);

void ui_confirm_delete(UI *ui, const char *id, const char *message);

void ui_add_recipient(UI *ui, const char **names, int count);
void ui_recipient_token(UI *ui, const char *name, const char *title);
void ui_send_state(UI *ui, int count, int locked, int retry, int has_file, int can_add);

void ui_feedback(UI *ui, const char *headline, int visible, int expanded);
void ui_feedback_delivery(UI *ui, const char *name, const char *outcome, const char *id);
