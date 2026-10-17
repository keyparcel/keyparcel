#include "ui.h"
#include "clipboard.h"
#include "_cgo_export.h"

struct UI {
    int refs;
    guint watch_source;
    uintptr_t handle;
    GtkApplication *app;
    GtkWindow *window;
    GtkStack *pages;
    GtkWidget *body, *status, *spinner, *cancel, *picker;
    GHashTable *fields, *inbox_rows, *device_rows, **current_rows, *next_rows;
    GtkWidget *row_box, *last_row;
    gboolean closed, destroyed, has_file, send_locked;
    int send_count;
};
typedef struct { UI *ui; char *action, *arg; GtkWidget *entry; } Action;

void ui_ref(UI *ui) { g_atomic_int_inc(&ui->refs); }
void ui_unref(UI *ui) {
    if (!g_atomic_int_dec_and_test(&ui->refs)) return;
    g_clear_object(&ui->window);
    g_clear_object(&ui->app);
    g_hash_table_unref(ui->fields);
    g_hash_table_unref(ui->inbox_rows); g_hash_table_unref(ui->device_rows);
    goRelease(ui->handle);
    g_free(ui);
}
int ui_pending(UI *ui) { return g_atomic_int_get(&ui->refs) - 1; }
void ui_iterate(void) { g_main_context_iteration(NULL, FALSE); }

static Action *action_new(UI *ui, const char *action, const char *arg) {
    Action *a = g_new0(Action, 1);
    a->ui = ui; a->action = g_strdup(action); a->arg = g_strdup(arg);
    return a;
}
static void action_free(gpointer data, GClosure *closure) {
    (void)closure;
    Action *a = data;
    g_free(a->action); g_free(a->arg); g_free(a);
}
static void clicked(GtkButton *button, gpointer data) {
    (void)button;
    Action *a = data;
    if (!a->ui->closed) goEvent(a->ui->handle, a->action, a->arg, "");
}
static GtkWidget *button(UI *ui, const char *label, const char *action, const char *arg) {
    GtkWidget *b = gtk_button_new_with_label(label);
    gtk_widget_set_halign(b, GTK_ALIGN_START);
    if (strcmp(action, "send-text") == 0 || strcmp(action, "send-file") == 0 ||
        strcmp(action, "connect") == 0 || strcmp(action, "receive-text") == 0 || strcmp(action, "receive-file") == 0)
        gtk_widget_add_css_class(b, "suggested-action");
    g_signal_connect_data(b, "clicked", G_CALLBACK(clicked), action_new(ui, action, arg), action_free, 0);
    return b;
}
static void accessible(GtkWidget *widget, const char *label) {
    // Button labels otherwise take precedence over the explicit accessible name.
    gtk_accessible_reset_relation(GTK_ACCESSIBLE(widget), GTK_ACCESSIBLE_RELATION_LABELLED_BY);
    gtk_accessible_update_property(GTK_ACCESSIBLE(widget), GTK_ACCESSIBLE_PROPERTY_LABEL, label, -1);
}
static GtkWidget *label(const char *text) {
    GtkWidget *w = gtk_label_new(text);
    gtk_label_set_xalign(GTK_LABEL(w), 0);
    gtk_label_set_wrap(GTK_LABEL(w), TRUE);
    gtk_label_set_selectable(GTK_LABEL(w), TRUE);
    return w;
}
static void remember(UI *ui, const char *name, GtkWidget *widget) {
    g_hash_table_insert(ui->fields, g_strdup(name), widget);
}
static GtkWidget *field(UI *ui, GtkWidget *box, const char *name, const char *title) {
    gtk_box_append(GTK_BOX(box), label(title));
    GtkWidget *entry = gtk_entry_new();
    accessible(entry, title);
    remember(ui, name, entry);
    gtk_box_append(GTK_BOX(box), entry);
    return entry;
}
static GtkWidget *page(UI *ui, const char *name, const char *title) {
    GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 14);
    gtk_widget_add_css_class(box, "page");
    GtkWidget *heading = label(title);
    gtk_widget_add_css_class(heading, "page-title");
    gtk_box_append(GTK_BOX(box), heading);
    GtkWidget *scroll = gtk_scrolled_window_new();
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(scroll), box);
    gtk_stack_add_named(ui->pages, scroll, name);
    return box;
}
static GtkWidget *text_view(UI *ui, const char *name, gboolean editable) {
    GtkWidget *view = gtk_text_view_new();
    gtk_text_view_set_wrap_mode(GTK_TEXT_VIEW(view), GTK_WRAP_WORD_CHAR);
    gtk_text_view_set_editable(GTK_TEXT_VIEW(view), editable);
    gtk_text_view_set_left_margin(GTK_TEXT_VIEW(view), 14);
    gtk_text_view_set_right_margin(GTK_TEXT_VIEW(view), 14);
    gtk_text_view_set_top_margin(GTK_TEXT_VIEW(view), 12);
    gtk_text_view_set_bottom_margin(GTK_TEXT_VIEW(view), 12);
    accessible(view, editable ? "Message" : "Received text");
    remember(ui, name, view);
    GtkWidget *scroll = gtk_scrolled_window_new();
    gtk_widget_add_css_class(scroll, "message-editor");
    gtk_widget_set_size_request(scroll, -1, 180);
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(scroll), view);
    return scroll;
}
static void selected_copy(GtkTextView *view, gpointer data) {
    g_signal_stop_emission_by_name(view, "copy-clipboard");
    UI *ui = data;
    if (!ui->closed) goEvent(ui->handle, "copy-selection", "", "");
}

static void response(GtkDialog *dialog, int answer, gpointer data) {
    Action *a = data;
    UI *ui = a->ui;
    ui->picker = NULL;
    if (!ui->closed) {
        if (answer == GTK_RESPONSE_ACCEPT) {
            if (GTK_IS_FILE_CHOOSER(dialog)) {
                GFile *file = gtk_file_chooser_get_file(GTK_FILE_CHOOSER(dialog));
                char *path = file ? g_file_get_path(file) : NULL;
                if (path) goEvent(ui->handle, a->action, path, "");
                else goEvent(ui->handle, "error", "Only local files are supported", "");
                g_free(path);
                g_clear_object(&file);
            } else {
                const char *value = strcmp(a->action,"add-recipient")==0 ? a->arg : !a->entry ? "" : gtk_editable_get_text(GTK_EDITABLE(a->entry));
                goEvent(ui->handle, a->action, a->arg, (char *)value);
            }
        } else if (strcmp(a->action, "add-recipient") == 0) goEvent(ui->handle,"recipient-cancel","","");
        else if (strcmp(a->action, "delete-confirm") == 0) goEvent(ui->handle, "delete-cancel", "", "");
        else goEvent(ui->handle, "notice", "Selection cancelled; remote transfers retained", "");
    }
    gtk_window_destroy(GTK_WINDOW(dialog));
    ui_unref(ui);
}
static gboolean back_key(GtkEventControllerKey *controller, guint key, guint code, GdkModifierType state, gpointer data) {
    (void)controller; (void)code;
    UI *ui = data;
    if (key != GDK_KEY_Escape && !(key == GDK_KEY_Left && (state & GDK_ALT_MASK))) return FALSE;
    if (ui->picker) ui_cancel_picker(ui);
    else {
        GtkExpander *advanced = g_hash_table_lookup(ui->fields, "advanced");
        if (strcmp(gtk_stack_get_visible_child_name(ui->pages), "settings") == 0 && gtk_expander_get_expanded(advanced))
            gtk_expander_set_expanded(advanced, FALSE);
        else goEvent(ui->handle, "back", "", "");
    }
    return TRUE;
}
static void back_keys(UI *ui, GtkWidget *widget) {
    GtkEventController *keys = gtk_event_controller_key_new();
    g_signal_connect(keys, "key-pressed", G_CALLBACK(back_key), ui);
    gtk_widget_add_controller(widget, keys);
}
static void dialog_attach(UI *ui, GtkWidget *dialog, Action *a) {
    ui_ref(ui);
    ui->picker = dialog;
    gtk_widget_add_css_class(dialog, "keyparcel");
    if (!GTK_IS_FILE_CHOOSER(dialog)) {
        GtkWidget *content = gtk_dialog_get_content_area(GTK_DIALOG(dialog));
        gtk_box_set_spacing(GTK_BOX(content), 14);
        gtk_widget_set_margin_start(content, 24); gtk_widget_set_margin_end(content, 24);
        gtk_widget_set_margin_top(content, 20); gtk_widget_set_margin_bottom(content, 20);
    }
    back_keys(ui, dialog);
    g_signal_connect_data(dialog, "response", G_CALLBACK(response), a, action_free, 0);
    gtk_window_present(GTK_WINDOW(dialog));
}
void ui_picker(UI *ui, int save, const char *filename) {
    if (ui->picker) return;
    // shortcut: GTK 4 deprecated chooser avoids the portal race; migrate before GTK 5.
    GtkWidget *dialog = gtk_file_chooser_dialog_new(save ? "Save received file" : "Choose a file", ui->window,
        save ? GTK_FILE_CHOOSER_ACTION_SAVE : GTK_FILE_CHOOSER_ACTION_OPEN,
        "Cancel", GTK_RESPONSE_CANCEL, save ? "Save" : "Open", GTK_RESPONSE_ACCEPT, NULL);
    gtk_window_set_modal(GTK_WINDOW(dialog), TRUE);
    if (save) gtk_file_chooser_set_current_name(GTK_FILE_CHOOSER(dialog), filename);
    dialog_attach(ui, dialog, action_new(ui, save ? "save-path" : "send-path", ""));
}
void ui_cancel_picker(UI *ui) { if (ui->picker) gtk_dialog_response(GTK_DIALOG(ui->picker), GTK_RESPONSE_CANCEL); }
int ui_picker_pending(UI *ui) { return ui->picker != NULL; }

void ui_confirm(UI *ui, const char *action, const char *message) {
    GtkWidget *d = gtk_dialog_new_with_buttons("Confirm action", ui->window, GTK_DIALOG_MODAL,
        "Cancel", GTK_RESPONSE_CANCEL, "Confirm", GTK_RESPONSE_ACCEPT, NULL);
    gtk_box_append(GTK_BOX(gtk_dialog_get_content_area(GTK_DIALOG(d))), label(message));
    dialog_attach(ui, d, action_new(ui, action, ""));
}
void ui_confirm_delete(UI *ui, const char *id, const char *message) {
    if (ui->picker) return;
    GtkWidget *d = gtk_dialog_new_with_buttons("Delete inbox copy?", ui->window, GTK_DIALOG_MODAL,
        "Cancel", GTK_RESPONSE_CANCEL, "Delete", GTK_RESPONSE_ACCEPT, NULL);
    gtk_dialog_set_default_response(GTK_DIALOG(d), GTK_RESPONSE_CANCEL);
    gtk_box_append(GTK_BOX(gtk_dialog_get_content_area(GTK_DIALOG(d))), label(message));
    dialog_attach(ui, d, action_new(ui, "delete-confirm", id));
}
void ui_approve(UI *ui, const char *fp, const char *message, const char *name) {
    GtkWidget *d = gtk_dialog_new_with_buttons("Approve device", ui->window, GTK_DIALOG_MODAL,
        "Cancel", GTK_RESPONSE_CANCEL, "Approve", GTK_RESPONSE_ACCEPT, NULL);
    GtkWidget *box = gtk_dialog_get_content_area(GTK_DIALOG(d));
    gtk_box_append(GTK_BOX(box), label(message));
    Action *a = action_new(ui, "approve", fp);
    a->entry = gtk_entry_new();
    gtk_editable_set_text(GTK_EDITABLE(a->entry), name);
    accessible(a->entry, "Peer name");
    gtk_box_append(GTK_BOX(box), label("Name after verifying the fingerprint"));
    gtk_box_append(GTK_BOX(box), a->entry);
    dialog_attach(ui, d, a);
}

void ui_details(UI *ui, const char *id) {
    if (ui->picker) return;
    GtkWidget *d = gtk_dialog_new_with_buttons("Transfer details", ui->window, GTK_DIALOG_MODAL,
        "Close", GTK_RESPONSE_CANCEL, NULL);
    GtkWidget *box = gtk_dialog_get_content_area(GTK_DIALOG(d));
    gtk_box_append(GTK_BOX(box), label("Transfer ID"));
    gtk_box_append(GTK_BOX(box), label(id));
    gtk_box_append(GTK_BOX(box), button(ui, "Copy transfer ID", "copy-id", id));
    dialog_attach(ui, d, action_new(ui, "", ""));
}

static void feedback_expanded(GObject *object, GParamSpec *spec, gpointer data) {
    (void)spec;
    UI *ui=data;
    if (!ui->closed) goEvent(ui->handle,"feedback-expanded",gtk_expander_get_expanded(GTK_EXPANDER(object)) ? "true" : "false","");
}
static gboolean poll_tick(gpointer data) {
    UI *ui = data;
    if (ui->closed) return G_SOURCE_REMOVE;
    goEvent(ui->handle,"poll","",""); return G_SOURCE_CONTINUE;
}
static void release_ui(gpointer data) { ui_unref(data); }
void ui_watch(UI *ui) {
    ui_ref(ui);
    ui->watch_source = g_timeout_add_full(G_PRIORITY_DEFAULT,500,poll_tick,ui,release_ui);
}
static gboolean close_window(GtkWindow *window, gpointer data) {
    (void)window;
    UI *ui = data;
    if (!ui->closed) {
        ui->closed = TRUE;
        if (ui->watch_source) { g_source_remove(ui->watch_source); ui->watch_source = 0; }
        ui_cancel_picker(ui);
        goEvent(ui->handle, "close", "", "");
        g_application_quit(G_APPLICATION(ui->app));
    }
    return TRUE;
}
static void present(GApplication *app, gpointer data) { (void)app; gtk_window_present(((UI *)data)->window); }
UI *ui_new(uintptr_t handle, const char *icon) {
    gtk_init();
    gtk_window_set_default_icon_name("dev.keyparcel");
    GtkCssProvider *style = gtk_css_provider_new();
    gtk_css_provider_load_from_data(style,
        ".keyparcel .brand { font-size: 1.6em; font-weight: 700; }"
        ".keyparcel .page-title { font-size: 1.8em; font-weight: 700; margin-bottom: 2px; }"
        ".keyparcel .page { padding: 24px; }"
        ".keyparcel .header { padding: 20px 24px 16px; }"
        ".keyparcel .navigation { padding: 0 24px 16px; border-bottom: 1px solid alpha(@theme_fg_color, 0.12); }"
        ".keyparcel .navigation button { border-radius: 18px; padding: 7px 18px; }"
        ".keyparcel .navigation button:checked { background: @theme_selected_bg_color; color: @theme_selected_fg_color; }"
        ".keyparcel .card { background: @theme_base_color; border: 1px solid alpha(@theme_fg_color, 0.14); border-radius: 12px; padding: 16px; }"
        ".keyparcel .message-editor { border: 1px solid alpha(@theme_fg_color, 0.22); border-radius: 8px; }"
        ".keyparcel .key-material { font-family: monospace; font-size: 0.9em; }"
        ".keyparcel .connection-status { font-size: 0.9em; }"
        ".keyparcel button { padding: 7px 14px; }", -1);
    gtk_style_context_add_provider_for_display(gdk_display_get_default(), GTK_STYLE_PROVIDER(style), GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);
    g_object_unref(style);
    UI *ui = g_new0(UI, 1);
    ui->refs = 1; ui->handle = handle;
    ui->fields = g_hash_table_new_full(g_str_hash, g_str_equal, g_free, NULL);
    ui->inbox_rows = g_hash_table_new_full(g_str_hash,g_str_equal,g_free,NULL);
    ui->device_rows = g_hash_table_new_full(g_str_hash,g_str_equal,g_free,NULL);
    ui->app = gtk_application_new("dev.keyparcel", G_APPLICATION_NON_UNIQUE);
    GError *error = NULL;
    if (!g_application_register(G_APPLICATION(ui->app), NULL, &error)) {
        g_printerr("GTK initialization: %s\n", error->message); g_clear_error(&error); ui_unref(ui); return NULL;
    }
    ui->window = GTK_WINDOW(gtk_application_window_new(ui->app));
    gtk_widget_add_css_class(GTK_WIDGET(ui->window), "keyparcel");
    g_object_ref(ui->window);
    gtk_window_set_title(ui->window, "keyparcel");
    gtk_window_set_default_size(ui->window, 880, 720);
    GtkWidget *outer = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    GtkWidget *header = gtk_box_new(GTK_ORIENTATION_VERTICAL, 10);
    gtk_widget_add_css_class(header, "header");
    GtkWidget *brand_row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 12);
    GBytes *bytes = g_bytes_new(icon, strlen(icon));
    GIcon *brand_icon = g_bytes_icon_new(bytes);
    GtkWidget *mark = gtk_image_new_from_gicon(brand_icon);
    gtk_image_set_pixel_size(GTK_IMAGE(mark), 40);
    gtk_accessible_update_state(GTK_ACCESSIBLE(mark), GTK_ACCESSIBLE_STATE_HIDDEN, TRUE, -1);
    gtk_box_append(GTK_BOX(brand_row), mark);
    g_object_unref(brand_icon); g_bytes_unref(bytes);
    GtkWidget *brand = label("keyparcel"); gtk_widget_add_css_class(brand, "brand");
    gtk_label_set_selectable(GTK_LABEL(brand), FALSE);
    gtk_widget_set_hexpand(brand, TRUE); gtk_box_append(GTK_BOX(brand_row), brand);
    GtkWidget *sync = label("Not connected"); remember(ui, "sync", sync);
    gtk_widget_add_css_class(sync, "dim-label"); gtk_widget_add_css_class(sync, "connection-status");
    accessible(sync, "Connection status"); gtk_box_append(GTK_BOX(brand_row), sync);
    gtk_box_append(GTK_BOX(header), brand_row);
    GtkWidget *progress = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 12);
    ui->spinner = gtk_spinner_new(); ui->status = label("Loading configuration…");
    gtk_widget_set_hexpand(ui->status, TRUE);
    ui->cancel = button(ui, "Cancel operation", "cancel", "");
    gtk_widget_add_css_class(ui->cancel, "flat");
    gtk_widget_set_sensitive(ui->cancel, FALSE);
    gtk_box_append(GTK_BOX(progress), ui->spinner); gtk_box_append(GTK_BOX(progress), ui->status);
    gtk_box_append(GTK_BOX(progress), ui->cancel); gtk_box_append(GTK_BOX(header), progress);
    gtk_box_append(GTK_BOX(outer), header);
    ui->body = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    GtkWidget *nav = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    gtk_widget_add_css_class(nav, "navigation");
    const char *names[] = {"inbox", "send", "devices", "settings"};
    const char *titles[] = {"Inbox", "Send", "Devices", "Settings"};
    GtkToggleButton *group = NULL;
    for (int i=0; i<4; i++) {
        GtkWidget *b = gtk_toggle_button_new_with_label(titles[i]);
        if (group) gtk_toggle_button_set_group(GTK_TOGGLE_BUTTON(b), group);
        else group = GTK_TOGGLE_BUTTON(b);
        g_signal_connect_data(b, "clicked", G_CALLBACK(clicked), action_new(ui, "page", names[i]), action_free, 0);
        char *name = g_strdup_printf("nav-%s", names[i]); remember(ui, name, b); g_free(name);
        if (i == 3) { gtk_widget_set_hexpand(b, TRUE); gtk_widget_set_halign(b, GTK_ALIGN_END); }
        gtk_box_append(GTK_BOX(nav), b);
    }
    GtkWidget *back = button(ui, "Back", "back", ""); remember(ui, "back", back);
    gtk_box_append(GTK_BOX(nav), back);
    gtk_box_append(GTK_BOX(ui->body), nav);
    ui->pages = GTK_STACK(gtk_stack_new()); gtk_widget_set_vexpand(GTK_WIDGET(ui->pages), TRUE);
    gtk_box_append(GTK_BOX(ui->body), GTK_WIDGET(ui->pages)); gtk_box_append(GTK_BOX(outer), ui->body);

    GtkWidget *inbox = page(ui, "inbox", "Inbox");
    GtkWidget *host = label(""); remember(ui, "active-host", host); gtk_box_append(GTK_BOX(inbox), host);
    gtk_box_append(GTK_BOX(inbox), button(ui, "Refresh inbox", "inbox", ""));
    GtkWidget *items = gtk_box_new(GTK_ORIENTATION_VERTICAL, 14); remember(ui, "items", items);
    gtk_box_append(GTK_BOX(inbox), items);

    GtkWidget *settings = page(ui, "settings", "Settings — shared with the CLI");
    gtk_box_append(GTK_BOX(settings), label("SSH target or saved host"));
    GtkWidget *hosts = gtk_combo_box_text_new_with_entry(); remember(ui, "hosts", hosts);
    remember(ui, "target", gtk_combo_box_get_child(GTK_COMBO_BOX(hosts)));
    accessible(gtk_combo_box_get_child(GTK_COMBO_BOX(hosts)), "SSH target"); gtk_box_append(GTK_BOX(settings), hosts);
    gtk_box_append(GTK_BOX(settings), button(ui, "Use saved host", "use-host", ""));
    field(ui, settings, "identity", "Identity file");
    GtkWidget *actions = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    gtk_box_append(GTK_BOX(actions), button(ui, "Save settings", "setup", ""));
    gtk_box_append(GTK_BOX(actions), button(ui, "Connect", "connect", "")); gtk_box_append(GTK_BOX(settings), actions);
    GtkWidget *pub = label(""); remember(ui, "public-key", pub); gtk_box_append(GTK_BOX(settings), pub);
    GtkWidget *fp = label(""); remember(ui, "identity-fingerprint", fp); gtk_box_append(GTK_BOX(settings), fp);
    gtk_widget_add_css_class(pub, "key-material"); gtk_widget_add_css_class(fp, "key-material");
    gtk_box_append(GTK_BOX(settings), label("Enroll this existing public key in the server's authorized_keys. Server approval is stored by OpenSSH."));
    GtkWidget *copy_actions = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    gtk_box_append(GTK_BOX(copy_actions), button(ui, "Copy enrollment key", "copy-key", ""));
    gtk_box_append(GTK_BOX(copy_actions), button(ui, "Copy identity fingerprint", "copy-identity", ""));
    gtk_box_append(GTK_BOX(settings), copy_actions);
    GtkWidget *advanced = gtk_expander_new("Advanced"); remember(ui, "advanced", advanced);
    GtkWidget *advanced_box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 8);
    field(ui, advanced_box, "root", "Remote storage");
    field(ui, advanced_box, "discovery", "Discovery file");
    gtk_box_append(GTK_BOX(advanced_box), label("OpenSSH configuration override (empty uses OpenSSH defaults)"));
    GtkWidget *ssh_config = label(""); remember(ui, "ssh-config", ssh_config); gtk_box_append(GTK_BOX(advanced_box), ssh_config);
    gtk_box_append(GTK_BOX(advanced_box), label("Server keys are managed in OpenSSH known_hosts. Identity and server fingerprints remain visible during trust approval."));
    gtk_expander_set_child(GTK_EXPANDER(advanced), advanced_box); gtk_box_append(GTK_BOX(settings), advanced);

    GtkWidget *devices = page(ui, "devices", "Devices and approvals");
    gtk_box_append(GTK_BOX(devices), button(ui, "Refresh devices", "devices", ""));
    GtkWidget *list = gtk_box_new(GTK_ORIENTATION_VERTICAL, 14); remember(ui, "device-list", list);
    gtk_box_append(GTK_BOX(devices), list);

    GtkWidget *send = page(ui, "send", "Send");
    gtk_box_append(GTK_BOX(send), label("Recipient"));
    GtkWidget *tokens = gtk_flow_box_new(); gtk_flow_box_set_selection_mode(GTK_FLOW_BOX(tokens),GTK_SELECTION_NONE);
    remember(ui,"recipient-tokens",tokens); gtk_box_append(GTK_BOX(send),tokens);
    GtkWidget *add = button(ui,"Choose recipient…","add-recipient-request",""); remember(ui,"add-recipient",add); gtk_flow_box_append(GTK_FLOW_BOX(tokens),add);
    GtkWidget *feedback=gtk_box_new(GTK_ORIENTATION_VERTICAL,8); remember(ui,"send-feedback",feedback);
    GtkWidget *disclosure=gtk_expander_new(""); remember(ui,"feedback-disclosure",disclosure);
    GtkWidget *details=gtk_box_new(GTK_ORIENTATION_VERTICAL,8); remember(ui,"feedback-details",details);
    gtk_expander_set_child(GTK_EXPANDER(disclosure),details);
    g_signal_connect(disclosure,"notify::expanded",G_CALLBACK(feedback_expanded),ui);
    gtk_box_append(GTK_BOX(feedback),disclosure);
    GtkWidget *retry=button(ui,"Retry failed recipients","retry-send",""); remember(ui,"retry-send",retry); gtk_widget_set_visible(retry,FALSE); gtk_box_append(GTK_BOX(feedback),retry);
    GtkWidget *edit=button(ui,"Edit draft","edit-send",""); remember(ui,"edit-send",edit); gtk_widget_set_visible(edit,FALSE); gtk_box_append(GTK_BOX(feedback),edit);
    gtk_widget_set_visible(feedback,FALSE); gtk_box_append(GTK_BOX(send),feedback);
    gtk_box_append(GTK_BOX(send), text_view(ui, "draft", TRUE));
    GtkWidget *send_text = button(ui, "Send text", "send-text", ""); remember(ui, "send-text", send_text);
    gtk_widget_set_sensitive(send_text, FALSE); gtk_box_append(GTK_BOX(send), send_text);
    GtkWidget *selected = label("No file selected"); remember(ui, "selected-file", selected); gtk_box_append(GTK_BOX(send), selected);
    GtkWidget *file_actions = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    GtkWidget *choose = button(ui, "Choose file", "choose-file", ""); remember(ui,"choose-file",choose); gtk_box_append(GTK_BOX(file_actions),choose);
    GtkWidget *send_file = button(ui, "Send file", "send-file", ""); remember(ui, "send-file", send_file);
    gtk_widget_set_sensitive(send_file, FALSE); gtk_box_append(GTK_BOX(file_actions), send_file);
    gtk_box_append(GTK_BOX(send), file_actions);
    gtk_box_append(GTK_BOX(send), button(ui, "Discard draft", "discard-draft", ""));

    GtkWidget *view = page(ui, "text", "Parcel — remote copy retained");
    gtk_box_append(GTK_BOX(view), text_view(ui, "received", FALSE));
    g_signal_connect(g_hash_table_lookup(ui->fields, "received"), "copy-clipboard", G_CALLBACK(selected_copy), ui);
    GtkWidget *notice = label(""); remember(ui, "view-notice", notice); gtk_box_append(GTK_BOX(view), notice);
    GtkWidget *copy_message=button(ui,"Copy complete message","copy-message","");remember(ui,"copy-message",copy_message);gtk_box_append(GTK_BOX(view),copy_message);
    gtk_box_append(GTK_BOX(view),button(ui,"Save file","save-view",""));
    gtk_box_append(GTK_BOX(view), button(ui, "Remove & close", "remove", ""));
    gtk_box_append(GTK_BOX(view), button(ui, "Back to Inbox", "result-back", ""));
    GtkWidget *saved = page(ui, "saved", "File saved — remote copy retained");
    GtkWidget *dest = label(""); remember(ui, "saved-path", dest); gtk_box_append(GTK_BOX(saved), dest);
    gtk_box_append(GTK_BOX(saved), button(ui, "Remove & close", "remove", ""));
    GtkWidget *saved_back=button(ui,"Back to Inbox","result-back","");remember(ui,"saved-back",saved_back);gtk_box_append(GTK_BOX(saved),saved_back);

    gtk_window_set_child(ui->window, outer);
    back_keys(ui, GTK_WIDGET(ui->window));
    g_signal_connect(ui->window, "close-request", G_CALLBACK(close_window), ui);
    g_signal_connect(ui->app, "activate", G_CALLBACK(present), ui);
    return ui;
}
int ui_run(UI *ui) { return g_application_run(G_APPLICATION(ui->app), 0, NULL); }
void ui_shutdown(UI *ui) {
    if (ui->destroyed) return;
    close_window(ui->window, ui);
    gtk_window_destroy(ui->window);
    ui->destroyed = TRUE;
}
void ui_page(UI *ui, const char *name) {
    gtk_stack_set_visible_child_name(ui->pages, name);
    const char *section = (strcmp(name, "text") == 0 || strcmp(name, "saved") == 0) ? "inbox" : name;
    char *nav = g_strdup_printf("nav-%s", section);
    gtk_toggle_button_set_active(g_hash_table_lookup(ui->fields, nav), TRUE); g_free(nav);
    gtk_widget_set_sensitive(g_hash_table_lookup(ui->fields, "back"), strcmp(name, "settings") == 0 || strcmp(name, "text") == 0 || strcmp(name, "saved") == 0);
}
void ui_message(UI *ui, const char *text) { gtk_label_set_text(GTK_LABEL(ui->status), text); }
void ui_busy(UI *ui, int busy) {
    gtk_widget_set_sensitive(ui->body, !busy); gtk_widget_set_sensitive(ui->cancel, busy);
    if (busy) gtk_spinner_start(GTK_SPINNER(ui->spinner)); else gtk_spinner_stop(GTK_SPINNER(ui->spinner));
}
const char *ui_get(UI *ui, const char *name) {
    GtkWidget *w = g_hash_table_lookup(ui->fields, name);
    if (GTK_IS_COMBO_BOX(w)) { const char *id = gtk_combo_box_get_active_id(GTK_COMBO_BOX(w)); return id ? id : ""; }
    if (GTK_IS_EDITABLE(w)) return gtk_editable_get_text(GTK_EDITABLE(w));
    if (GTK_IS_LABEL(w)) return gtk_label_get_text(GTK_LABEL(w));
    return "";
}
void ui_set(UI *ui, const char *name, const char *text) {
    GtkWidget *w = g_hash_table_lookup(ui->fields, name);
    if (GTK_IS_EDITABLE(w)) gtk_editable_set_text(GTK_EDITABLE(w), text);
    else if (GTK_IS_LABEL(w)) gtk_label_set_text(GTK_LABEL(w), text);
}
char *ui_draft(UI *ui) {
    GtkTextBuffer *b = gtk_text_view_get_buffer(GTK_TEXT_VIEW(g_hash_table_lookup(ui->fields, "draft")));
    GtkTextIter start, end; gtk_text_buffer_get_bounds(b, &start, &end);
    return gtk_text_buffer_get_text(b, &start, &end, FALSE);
}
char *ui_selection(UI *ui) {
    GtkTextBuffer *b = gtk_text_view_get_buffer(GTK_TEXT_VIEW(g_hash_table_lookup(ui->fields, "received")));
    GtkTextIter start, end;
    if (!gtk_text_buffer_get_selection_bounds(b, &start, &end)) return NULL;
    return gtk_text_buffer_get_text(b, &start, &end, FALSE);
}
void ui_clear(UI *ui, const char *name) {
    if (strcmp(name,"items") == 0) g_hash_table_remove_all(ui->inbox_rows);
    if (strcmp(name,"device-list") == 0) g_hash_table_remove_all(ui->device_rows);
    GtkWidget *w = g_hash_table_lookup(ui->fields, name);
    if (GTK_IS_COMBO_BOX_TEXT(w)) { gtk_combo_box_text_remove_all(GTK_COMBO_BOX_TEXT(w)); return; }
    GtkWidget *child;
    if (GTK_IS_FLOW_BOX(w)) {
        for (child=gtk_widget_get_first_child(w);child;) {
            GtkWidget *next=gtk_widget_get_next_sibling(child);
            if (gtk_flow_box_child_get_child(GTK_FLOW_BOX_CHILD(child)) != g_hash_table_lookup(ui->fields,"add-recipient")) gtk_flow_box_remove(GTK_FLOW_BOX(w),child);
            child=next;
        }
        return;
    }
    while ((child = gtk_widget_get_first_child(w))) gtk_box_remove(GTK_BOX(w), child);
}
void ui_host(UI *ui, const char *name) { gtk_combo_box_text_append_text(GTK_COMBO_BOX_TEXT(g_hash_table_lookup(ui->fields, "hosts")), name); }
void ui_send_ready(UI *ui, int has_file) {
    ui->has_file = has_file;
    gboolean selected = ui->send_count > 0;
    gtk_widget_set_sensitive(g_hash_table_lookup(ui->fields, "send-text"), selected && !ui->send_locked);
    gtk_widget_set_sensitive(g_hash_table_lookup(ui->fields, "send-file"), selected && has_file && !ui->send_locked);
}
void ui_clear_draft(UI *ui, int file) {
    if (!file) gtk_text_buffer_set_text(gtk_text_view_get_buffer(GTK_TEXT_VIEW(g_hash_table_lookup(ui->fields, "draft"))), "", -1);
}
// Retain unchanged rows across snapshots, including their focus/text selection.
void ui_rows_begin(UI *ui, const char *name) {
    ui->current_rows = strcmp(name,"items") == 0 ? &ui->inbox_rows : &ui->device_rows;
    ui->next_rows = g_hash_table_new_full(g_str_hash,g_str_equal,g_free,NULL);
    ui->row_box = g_hash_table_lookup(ui->fields,name); ui->last_row = NULL;
}
static void keep_row(UI *ui, const char *key, GtkWidget *row) {
    g_hash_table_insert(ui->next_rows,g_strdup(key),row);
    gtk_box_reorder_child_after(GTK_BOX(ui->row_box),row,ui->last_row); ui->last_row = row;
}
static GtkWidget *existing_row(UI *ui, const char *key, const char *content) {
    GtkWidget *row = g_hash_table_lookup(*ui->current_rows,key);
    if (row && strcmp(g_object_get_data(G_OBJECT(row),"content"),content) == 0) {
        keep_row(ui,key,row); return row;
    }
    if (row) { gtk_box_remove(GTK_BOX(ui->row_box),row); g_hash_table_remove(*ui->current_rows,key); }
    return NULL;
}
static void add_row(UI *ui, const char *key, const char *content, GtkWidget *row) {
    g_object_set_data_full(G_OBJECT(row),"content",g_strdup(content),g_free);
    gtk_box_append(GTK_BOX(ui->row_box),row); keep_row(ui,key,row);
}
void ui_rows_end(UI *ui) {
    GHashTableIter iter; gpointer key, value;
    g_hash_table_iter_init(&iter,*ui->current_rows);
    while (g_hash_table_iter_next(&iter,&key,&value))
        if (!g_hash_table_contains(ui->next_rows,key)) gtk_box_remove(GTK_BOX(ui->row_box),value);
    g_hash_table_unref(*ui->current_rows); *ui->current_rows = ui->next_rows; ui->next_rows = NULL;
}
void ui_item(UI *ui, const char *id, const char *title, const char *detail, int can_view) {
    char *key = id[0] ? g_strdup(id) : g_strdup_printf("notice:%s:%s",title,detail);
    char *content = g_strdup_printf("%s\n%s\n%d",title,detail,can_view);
    if (existing_row(ui,key,content)) { g_free(key); g_free(content); return; }
    GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 6);
    gtk_widget_add_css_class(box, "card");
    GtkWidget *primary = label(title);
    PangoAttrList *attrs = pango_attr_list_new(); pango_attr_list_insert(attrs, pango_attr_weight_new(PANGO_WEIGHT_BOLD));
    gtk_label_set_attributes(GTK_LABEL(primary), attrs); pango_attr_list_unref(attrs);
    gtk_box_append(GTK_BOX(box), primary);
    GtkWidget *secondary = label(detail); gtk_widget_add_css_class(secondary, "dim-label"); gtk_box_append(GTK_BOX(box), secondary);
    if (id[0]) {
        GtkWidget *b=button(ui,"View","receive-text",id);
        char *name=g_strdup_printf("View transfer %s",id);accessible(b,name);g_free(name);
        GtkWidget *view_box=gtk_box_new(GTK_ORIENTATION_HORIZONTAL,0);
        gtk_box_append(GTK_BOX(view_box),b);gtk_widget_set_sensitive(b,can_view);
        if (!can_view) {
            const char *why="Too large to preview—save the file instead";
            gtk_widget_set_tooltip_text(view_box,why);gtk_widget_set_focusable(view_box,TRUE);
            GtkWidget *explanation=label(why);gtk_widget_add_css_class(explanation,"dim-label");gtk_box_append(GTK_BOX(box),explanation);
            gtk_accessible_update_property(GTK_ACCESSIBLE(view_box),GTK_ACCESSIBLE_PROPERTY_LABEL,why,GTK_ACCESSIBLE_PROPERTY_DESCRIPTION,why,-1);
        }
        GtkWidget *actions = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
        gtk_box_append(GTK_BOX(actions),view_box);
        GtkWidget *save=button(ui,"Save file","receive-file",id);
        name=g_strdup_printf("Save transfer %s",id);accessible(save,name);g_free(name);
        gtk_box_append(GTK_BOX(actions),save);
        GtkWidget *details = button(ui, "Details", "details", id);
        name = g_strdup_printf("Details transfer %s", id); accessible(details, name); g_free(name);
        gtk_box_append(GTK_BOX(actions), details);
        GtkWidget *del = button(ui, "Delete", "delete-request", id);
        name = g_strdup_printf("Delete transfer %s", id); accessible(del, name); g_free(name);
        gtk_box_append(GTK_BOX(actions), del); gtk_box_append(GTK_BOX(box), actions);
    }
    add_row(ui,key,content,box); g_free(key); g_free(content);
}
void ui_device(UI *ui, const char *fp, const char *detail, int supported, int trusted, int self) {
    char *content = g_strdup_printf("%s\n%d%d%d",detail,supported,trusted,self);
    if (existing_row(ui,fp,content)) { g_free(content); return; }
    GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 6);
    gtk_widget_add_css_class(box, "card");
    gtk_box_append(GTK_BOX(box), label(detail));
    GtkWidget *b = button(ui, "Copy fingerprint", "copy-peer", fp);
    GtkWidget *actions = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    char *name = g_strdup_printf("Copy fingerprint %s", fp); accessible(b, name); g_free(name); gtk_box_append(GTK_BOX(actions), b);
    if (!self && supported) {
        b = button(ui, trusted ? "Forget approval" : "Approve device", trusted ? "forget-request" : "approve-request", fp);
        name = g_strdup_printf("%s device %s", trusted ? "Forget" : "Approve", fp); accessible(b, name); g_free(name);
        gtk_box_append(GTK_BOX(actions), b);
    }
    gtk_box_append(GTK_BOX(box), actions);
    add_row(ui,fp,content,box); g_free(content);
}
void ui_view(UI *ui, const char *text, const char *notice, int eligible) {
    gtk_widget_set_visible(gtk_widget_get_parent(g_hash_table_lookup(ui->fields,"received")),eligible);
    gtk_widget_set_visible(g_hash_table_lookup(ui->fields,"copy-message"),eligible);
    GtkTextBuffer *b = gtk_text_view_get_buffer(GTK_TEXT_VIEW(g_hash_table_lookup(ui->fields, "received")));
    gtk_text_buffer_set_text(b, text, -1); ui_set(ui, "view-notice", notice); ui_page(ui, "text");
}
void ui_clear_results(UI *ui) {
    GtkTextBuffer *b = gtk_text_view_get_buffer(GTK_TEXT_VIEW(g_hash_table_lookup(ui->fields, "received")));
    gtk_text_buffer_set_text(b, "", -1);
    ui_set(ui, "view-notice", ""); ui_set(ui, "saved-path", "");
}
int ui_copy(UI *ui, const char *text) {
    GdkContentProvider *provider = plain_text_provider(text);
    int accepted = gdk_clipboard_set_content(gtk_widget_get_clipboard(GTK_WIDGET(ui->window)), provider);
    g_object_unref(provider);
    return accepted;
}

typedef struct { UI *ui; uintptr_t handle; } Posted;
static gboolean dispatch(gpointer data) { goDispatch(((Posted *)data)->handle); return G_SOURCE_REMOVE; }
static void posted_free(gpointer data) { Posted *p = data; goRelease(p->handle); ui_unref(p->ui); g_free(p); }
void ui_post(UI *ui, uintptr_t handle) {
    Posted *p = g_new(Posted, 1); *p = (Posted){ui, handle};
    g_idle_add_full(G_PRIORITY_DEFAULT, dispatch, p, posted_free);
}

typedef struct { GMainLoop *loop; int accepted; } Approval;
static void approve_response(GtkDialog *dialog, int answer, gpointer data) {
    Approval *a = data; a->accepted = answer == GTK_RESPONSE_ACCEPT;
    gtk_window_destroy(GTK_WINDOW(dialog)); g_main_loop_quit(a->loop);
}
int ui_askpass(const char *prompt) {
    gtk_init();
    Approval a = {g_main_loop_new(NULL, FALSE), 0};
    GtkWidget *d = gtk_dialog_new_with_buttons("SSH server approval", NULL, GTK_DIALOG_MODAL,
        "Reject", GTK_RESPONSE_CANCEL, "Approve server key", GTK_RESPONSE_ACCEPT, NULL);
    gtk_window_set_default_size(GTK_WINDOW(d), 640, 280);
    GtkWidget *box = gtk_dialog_get_content_area(GTK_DIALOG(d));
    gtk_box_append(GTK_BOX(box), label("Verify this OpenSSH fingerprint through a trusted source before approving."));
    gtk_box_append(GTK_BOX(box), label(prompt));
    g_signal_connect(d, "response", G_CALLBACK(approve_response), &a);
    gtk_window_present(GTK_WINDOW(d)); g_main_loop_run(a.loop); g_main_loop_unref(a.loop);
    return a.accepted;
}

void ui_sync(UI *ui, const char *state, const char *detail) {
    GtkWidget *w = g_hash_table_lookup(ui->fields,"sync");
    gtk_label_set_text(GTK_LABEL(w),state);
    accessible(w,"Connection status");
    gtk_widget_set_tooltip_text(w,detail);
    gtk_accessible_update_property(GTK_ACCESSIBLE(w),GTK_ACCESSIBLE_PROPERTY_DESCRIPTION,detail,-1);
}
void ui_refreshing(UI *ui, int refreshing) { gtk_widget_set_sensitive(ui->cancel,refreshing); }

static void recipient_chosen(GtkButton *button, gpointer data) {
    Action *a=data;
    g_free(a->arg);
    a->arg=g_strdup(g_object_get_data(G_OBJECT(button),"recipient"));
    gtk_dialog_response(GTK_DIALOG(gtk_widget_get_root(GTK_WIDGET(button))),GTK_RESPONSE_ACCEPT);
}
void ui_add_recipient(UI *ui, const char **names, int count) {
    if (ui->picker || count==0) return;
    GtkWidget *dialog=gtk_dialog_new_with_buttons("Choose recipient",ui->window,GTK_DIALOG_MODAL,"Cancel",GTK_RESPONSE_CANCEL,NULL);
    Action *a=action_new(ui,"add-recipient","");
    GtkWidget *list=gtk_box_new(GTK_ORIENTATION_VERTICAL,8);
    accessible(list,"Available recipients");
    for (int i=0;i<count;i++) {
        GtkWidget *choice=gtk_button_new_with_label(strcmp(names[i],"self")==0 ? "This device" : names[i]);
        g_object_set_data_full(G_OBJECT(choice),"recipient",g_strdup(names[i]),g_free);
        g_signal_connect(choice,"clicked",G_CALLBACK(recipient_chosen),a);
        gtk_box_append(GTK_BOX(list),choice);
    }
    GtkWidget *scroll=gtk_scrolled_window_new();
    gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(scroll),GTK_POLICY_NEVER,GTK_POLICY_AUTOMATIC);
    gtk_scrolled_window_set_min_content_height(GTK_SCROLLED_WINDOW(scroll),180);
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(scroll),list);
    gtk_box_append(GTK_BOX(gtk_dialog_get_content_area(GTK_DIALOG(dialog))),scroll);
    dialog_attach(ui,dialog,a);
}
void ui_recipient_token(UI *ui, const char *name, const char *title) {
    char *text=g_strdup_printf("%s ×",title);GtkWidget *b=button(ui,text,"remove-recipient",name);g_free(text);
    text=g_strdup_printf("Remove recipient %s",title);accessible(b,text);g_free(text);gtk_widget_add_css_class(b,"pill");
    GtkWidget *add=g_hash_table_lookup(ui->fields,"add-recipient");
    int position=gtk_flow_box_child_get_index(GTK_FLOW_BOX_CHILD(gtk_widget_get_parent(add)));
    gtk_flow_box_insert(GTK_FLOW_BOX(g_hash_table_lookup(ui->fields,"recipient-tokens")),b,position);
}
void ui_send_state(UI *ui, int count, int locked, int retry, int has_file, int can_add) {
    ui->send_locked=locked;ui->send_count=count;
    gtk_button_set_label(GTK_BUTTON(g_hash_table_lookup(ui->fields,"add-recipient")),count ? "+ Add another" : "Choose recipient…");
    gtk_widget_set_sensitive(g_hash_table_lookup(ui->fields,"add-recipient"),!locked && can_add);
    gtk_widget_set_sensitive(g_hash_table_lookup(ui->fields,"recipient-tokens"),!locked);
    gtk_widget_set_sensitive(g_hash_table_lookup(ui->fields,"choose-file"),!locked);
    gtk_text_view_set_editable(GTK_TEXT_VIEW(g_hash_table_lookup(ui->fields,"draft")),!locked);
    gtk_widget_set_visible(g_hash_table_lookup(ui->fields,"retry-send"),locked && retry);
    gtk_widget_set_visible(g_hash_table_lookup(ui->fields,"edit-send"),locked);
    char *label=count>1 ? g_strdup_printf("Send to %d devices",count) : g_strdup("Send");
    GtkWidget *text=g_hash_table_lookup(ui->fields,"send-text"),*file=g_hash_table_lookup(ui->fields,"send-file");
    gtk_button_set_label(GTK_BUTTON(text),label);gtk_button_set_label(GTK_BUTTON(file),label);g_free(label);
    char *tn=count>1 ? g_strdup_printf("Send text to %d devices",count) : g_strdup("Send text");
    char *fn=count>1 ? g_strdup_printf("Send file to %d devices",count) : g_strdup("Send file");
    accessible(text,tn);accessible(file,fn);g_free(tn);g_free(fn);
    ui_send_ready(ui,has_file);
}

void ui_feedback(UI *ui, const char *headline, int visible, int expanded) {
    GtkExpander *e=GTK_EXPANDER(g_hash_table_lookup(ui->fields,"feedback-disclosure"));
    gtk_expander_set_label(e,headline);
    accessible(GTK_WIDGET(e),headline);
    gtk_expander_set_expanded(e,expanded);
    gtk_widget_set_visible(g_hash_table_lookup(ui->fields,"send-feedback"),visible);
}
void ui_feedback_delivery(UI *ui, const char *name, const char *outcome, const char *id) {
    GtkWidget *box=g_hash_table_lookup(ui->fields,"feedback-details");
    char *text=g_strdup_printf("%s: %s",name,outcome);
    gtk_box_append(GTK_BOX(box),label(text));g_free(text);
    if (id[0]) {
        gtk_box_append(GTK_BOX(box),label(id));
        GtkWidget *copy=button(ui,"Copy ID","copy-id",id);
        text=g_strdup_printf("Copy sent transfer ID for %s",name);accessible(copy,text);g_free(text);
        gtk_box_append(GTK_BOX(box),copy);
    }
}

void ui_saved_origin(UI *ui,int parcel) {
    GtkWidget *b=g_hash_table_lookup(ui->fields,"saved-back");
    gtk_button_set_label(GTK_BUTTON(b),parcel ? "Back to parcel" : "Back to Inbox");
}
