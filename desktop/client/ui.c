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
    GtkWidget *body, *status, *spinner, *transfer_bar, *cancel, *picker, *progress, *confirmation;
    GHashTable *fields, *inbox_rows, *device_rows, **current_rows, *next_rows;
    GtkWidget *row_box, *last_row;
    gboolean closed, destroyed, has_file, send_locked, updating_settings, resetting_enrollment;
    int send_count;
    guint paste_generation;
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
        strcmp(action, "save-connect") == 0 || strcmp(action, "receive-text") == 0 || strcmp(action, "receive-file") == 0)
        gtk_widget_add_css_class(b, "suggested-action");
    g_signal_connect_data(b, "clicked", G_CALLBACK(clicked), action_new(ui, action, arg), action_free, 0);
    return b;
}
static void accessible(GtkWidget *widget, const char *label) {
    // Button labels otherwise take precedence over the explicit accessible name.
    gtk_accessible_reset_relation(GTK_ACCESSIBLE(widget), GTK_ACCESSIBLE_RELATION_LABELLED_BY);
    gtk_accessible_update_property(GTK_ACCESSIBLE(widget), GTK_ACCESSIBLE_PROPERTY_LABEL, label, -1);
}
static GtkWidget *icon_button(UI *ui, const char *icon, const char *name, const char *action) {
    GtkWidget *b = button(ui, "", action, "");
    gtk_button_set_child(GTK_BUTTON(b), gtk_image_new_from_icon_name(icon));
    accessible(b, name); gtk_widget_set_tooltip_text(b, name);
    gtk_widget_add_css_class(b, "flat");
    return b;
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
static void info_cancel_focus(GtkWidget *button) {
    guint source=GPOINTER_TO_UINT(g_object_get_data(G_OBJECT(button),"info-focus-source"));
    g_object_set_data(G_OBJECT(button),"info-focus-source",NULL);
    if (source) g_source_remove(source);
}
static gboolean info_focus_idle(gpointer data) {
    GtkEventControllerFocus *focus=data;
    GtkWidget *button=gtk_event_controller_get_widget(GTK_EVENT_CONTROLLER(focus));
    if (button) {
        g_object_set_data(G_OBJECT(button),"info-focus-source",NULL);
        if (gtk_event_controller_focus_contains_focus(focus) && gtk_widget_get_root(button) && !gtk_menu_button_get_active(GTK_MENU_BUTTON(button)))
            gtk_menu_button_popup(GTK_MENU_BUTTON(button));
    }
    return G_SOURCE_REMOVE;
}
static void info_focus_enter(GtkEventControllerFocus *focus,gpointer data) {
    (void)data;
    GtkWidget *button=gtk_event_controller_get_widget(GTK_EVENT_CONTROLLER(focus));
    if (gtk_menu_button_get_active(GTK_MENU_BUTTON(button))) return;
    info_cancel_focus(button);
    // Defer keyboard help until GTK finishes assigning focus.
    guint source=g_idle_add_full(G_PRIORITY_DEFAULT_IDLE,info_focus_idle,g_object_ref(focus),g_object_unref);
    g_object_set_data(G_OBJECT(button),"info-focus-source",GUINT_TO_POINTER(source));
}
static void info_focus_leave(GtkEventControllerFocus *focus,gpointer data) {
    (void)data;
    GtkWidget *button=gtk_event_controller_get_widget(GTK_EVENT_CONTROLLER(focus));
    if (button) { info_cancel_focus(button);gtk_menu_button_popdown(GTK_MENU_BUTTON(button)); }
}
static void info_active_changed(GtkMenuButton *button,GParamSpec *spec,GtkEventControllerFocus *focus) {
    (void)spec;
    info_cancel_focus(GTK_WIDGET(button));
    // Take pointer focus after activation, so focus help cannot preempt the click.
    if (gtk_menu_button_get_active(button) && !gtk_event_controller_focus_contains_focus(focus))
        gtk_widget_grab_focus(GTK_WIDGET(button));
}
static void info_label(UI *ui, GtkWidget *box, const char *title, const char *help, const char *name) {
    GtkWidget *row=gtk_box_new(GTK_ORIENTATION_HORIZONTAL,6);
    GtkWidget *caption=label(title);gtk_label_set_wrap(GTK_LABEL(caption),FALSE);
    gtk_label_set_selectable(GTK_LABEL(caption),FALSE);
    gtk_box_append(GTK_BOX(row),caption);
    GtkWidget *button=gtk_menu_button_new();
    gtk_widget_set_focus_on_click(button,FALSE);
    GtkWidget *symbol=gtk_image_new_from_icon_name("help-about-symbolic");
    gtk_image_set_pixel_size(GTK_IMAGE(symbol),14);gtk_menu_button_set_child(GTK_MENU_BUTTON(button),symbol);
    gtk_widget_add_css_class(button,"settings-info");gtk_widget_set_valign(button,GTK_ALIGN_CENTER);
    gtk_widget_add_css_class(button,"flat");
    accessible(button,name);gtk_widget_set_tooltip_text(button,help);
    gtk_accessible_update_property(GTK_ACCESSIBLE(button),GTK_ACCESSIBLE_PROPERTY_DESCRIPTION,help,-1);
    GtkWidget *popover=gtk_popover_new(), *text=label(help);
    gtk_label_set_selectable(GTK_LABEL(text),FALSE);gtk_label_set_max_width_chars(GTK_LABEL(text),52);
    gtk_popover_set_autohide(GTK_POPOVER(popover),FALSE);
    gtk_popover_set_child(GTK_POPOVER(popover),text);gtk_menu_button_set_popover(GTK_MENU_BUTTON(button),popover);
    remember(ui,name,popover);
    GtkEventController *focus=gtk_event_controller_focus_new();
    g_signal_connect(focus,"enter",G_CALLBACK(info_focus_enter),NULL);
    g_signal_connect(focus,"leave",G_CALLBACK(info_focus_leave),NULL);
    gtk_widget_add_controller(button,focus);
    g_signal_connect_object(button,"notify::active",G_CALLBACK(info_active_changed),focus,0);
    gtk_box_append(GTK_BOX(row),button);gtk_box_append(GTK_BOX(box),row);
}
static void host_selected(GtkComboBox *hosts, gpointer data) {
    UI *ui=data;
    gboolean popup=FALSE;g_object_get(hosts,"popup-shown",&popup,NULL);
    if (popup) return; // Load the committed choice, not intermediate keyboard navigation.
    const char *id=gtk_combo_box_get_active_id(hosts);
    const char *text=gtk_editable_get_text(GTK_EDITABLE(gtk_combo_box_get_child(hosts)));
    if (!ui->closed && !ui->updating_settings && id && strcmp(id,text)==0) goEvent(ui->handle,"host-selected",(char *)id,"");
}
static void host_popup_changed(GObject *object,GParamSpec *spec,gpointer data) {
    (void)spec;host_selected(GTK_COMBO_BOX(object),data);
}
static void access_method_changed(GtkComboBox *combo,gpointer data) {
    (void)combo;UI *ui=data;
    if (!ui->closed) goEvent(ui->handle,"access-method-changed","","");
}
static void identity_changed(GtkEditable *entry,gpointer data) {
    (void)entry;UI *ui=data;
    if (!ui->closed && !ui->updating_settings) goEvent(ui->handle,"identity-changed","","");
}
static void enrollment_changed(GObject *object,GParamSpec *spec,gpointer data) {
    (void)spec;UI *ui=data;
    if (!ui->closed && !ui->resetting_enrollment)
        goEvent(ui->handle,"enrollment-toggle",gtk_expander_get_expanded(GTK_EXPANDER(object)) ? "true" : "false","");
}
static GtkWidget *page(UI *ui, const char *name, const char *title) {
    GtkWidget *frame = g_object_new(GTK_TYPE_BOX,"orientation",GTK_ORIENTATION_VERTICAL,"spacing",14,
        "accessible-role",GTK_ACCESSIBLE_ROLE_GROUP,NULL);
    GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 14);
    gtk_widget_add_css_class(frame, "page");
    char *page_name=g_strdup_printf("Page: %s",title);accessible(frame,page_name);g_free(page_name);
    GtkWidget *heading = label(title);
    gtk_widget_add_css_class(heading, "page-title");
    GtkWidget *title_row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    gtk_box_append(GTK_BOX(title_row), heading);
    if (strcmp(name,"inbox")==0 || strcmp(name,"devices")==0)
        gtk_box_append(GTK_BOX(title_row), icon_button(ui,"view-refresh-symbolic",strcmp(name,"inbox")==0 ? "Refresh inbox" : "Refresh devices",name));
    gtk_box_append(GTK_BOX(frame), title_row);
    GtkWidget *error_box = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    GtkWidget *error_text = label("");
    gtk_widget_add_css_class(error_text,"error"); gtk_widget_set_hexpand(error_text,TRUE);
    char *key=g_strdup_printf("error-%s",name);remember(ui,key,error_box);g_free(key);
    g_object_set_data(G_OBJECT(error_box),"message",error_text);
    gtk_box_append(GTK_BOX(error_box),error_text);
    gtk_box_append(GTK_BOX(error_box),button(ui,"Dismiss error","dismiss-error",""));
    gtk_widget_set_visible(error_box,FALSE);gtk_box_append(GTK_BOX(frame),error_box);
    GtkWidget *scroll = gtk_scrolled_window_new();
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(scroll), box);
    gtk_widget_set_vexpand(scroll,TRUE);gtk_box_append(GTK_BOX(frame),scroll);
    gtk_stack_add_named(ui->pages, frame, name);
    g_object_set_data(G_OBJECT(box),"frame",frame);
    return box;
}
static void draft_changed(GtkTextBuffer *buffer, gpointer data) {
    (void)buffer;
    UI *ui = data;
    ui->paste_generation++;
    if (!ui->closed) ui_send_ready(ui, ui->has_file);
}
typedef struct { UI *ui; guint generation; int start, end; } Paste;
static void draft_cursor_changed(GtkTextBuffer *buffer, GtkTextIter *iter, GtkTextMark *mark, gpointer data) {
    (void)buffer; (void)iter;
    const char *name=gtk_text_mark_get_name(mark);
    if (g_strcmp0(name,"insert")==0 || g_strcmp0(name,"selection_bound")==0) ((UI *)data)->paste_generation++;
}
static void paste_ready(GObject *source, GAsyncResult *result, gpointer data) {
    Paste *p=data; UI *ui=p->ui;
    GError *error=NULL;
    char *text=gdk_clipboard_read_text_finish(GDK_CLIPBOARD(source),result,&error);
    GtkWidget *view=g_hash_table_lookup(ui->fields,"draft");
    if (!ui->closed && p->generation==ui->paste_generation && gtk_widget_is_sensitive(view) &&
        gtk_text_view_get_editable(GTK_TEXT_VIEW(view))) {
        if (text && *text) {
            GtkTextBuffer *buffer=gtk_text_view_get_buffer(GTK_TEXT_VIEW(view));
            GtkTextIter start,end;
            gtk_text_buffer_get_iter_at_offset(buffer,&start,p->start);
            gtk_text_buffer_get_iter_at_offset(buffer,&end,p->end);
            gtk_text_buffer_begin_user_action(buffer);
            gtk_text_buffer_delete(buffer,&start,&end);
            gtk_text_buffer_insert(buffer,&start,text,-1);
            gtk_text_buffer_place_cursor(buffer,&start);
            gtk_text_buffer_end_user_action(buffer);
            gtk_widget_grab_focus(view);
        } else if (error && !g_error_matches(error,G_IO_ERROR,G_IO_ERROR_NOT_SUPPORTED)) {
            goEvent(ui->handle,"error","Clipboard text is unavailable","");
        }
    }
    g_clear_error(&error);g_free(text);ui_unref(ui);g_free(p);
}
static void paste_clicked(GtkButton *button, gpointer data) {
    (void)button;
    UI *ui=data;
    GtkWidget *view=g_hash_table_lookup(ui->fields,"draft");
    if (ui->closed || !gtk_widget_is_sensitive(view) || !gtk_text_view_get_editable(GTK_TEXT_VIEW(view))) return;
    GtkTextBuffer *buffer=gtk_text_view_get_buffer(GTK_TEXT_VIEW(view));
    GtkTextIter start,end;
    if (!gtk_text_buffer_get_selection_bounds(buffer,&start,&end)) {
        gtk_text_buffer_get_iter_at_mark(buffer,&start,gtk_text_buffer_get_insert(buffer));end=start;
    }
    Paste *p=g_new0(Paste,1);p->ui=ui;p->generation=++ui->paste_generation;
    p->start=gtk_text_iter_get_offset(&start);p->end=gtk_text_iter_get_offset(&end);
    ui_ref(ui);
    gdk_clipboard_read_text_async(gtk_widget_get_clipboard(view),NULL,paste_ready,p);
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
        else goEvent(ui->handle, "picker-cancel", "", "");
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
        const char *help_names[]={"SSH target information","Identity file information","Remote access method information"};
        for (guint i=0;i<G_N_ELEMENTS(help_names);i++) {
            GtkWidget *help=g_hash_table_lookup(ui->fields,help_names[i]);
            if (help && gtk_widget_get_visible(help)) { gtk_popover_popdown(GTK_POPOVER(help)); return TRUE; }
        }
        GtkExpander *advanced = g_hash_table_lookup(ui->fields, "advanced");
        if (strcmp(gtk_stack_get_visible_child_name(ui->pages), "settings") == 0 && gtk_expander_get_expanded(advanced)) {
            gtk_widget_grab_focus(GTK_WIDGET(advanced));
            gtk_expander_set_expanded(advanced, FALSE);
        } else goEvent(ui->handle, "back", "", "");
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
void ui_identity_picker(UI *ui) {
    if (ui->picker) return;
    GtkWidget *dialog=gtk_file_chooser_dialog_new("Choose identity file",ui->window,GTK_FILE_CHOOSER_ACTION_OPEN,
        "Cancel",GTK_RESPONSE_CANCEL,"Open",GTK_RESPONSE_ACCEPT,NULL);
    gtk_window_set_modal(GTK_WINDOW(dialog),TRUE);
    dialog_attach(ui,dialog,action_new(ui,"identity-path",""));
}
void ui_settings_edit(UI *ui,int updating) { ui->updating_settings=updating; }
void ui_enrollment(UI *ui,const char *public_key,const char *fingerprint,const char *error) {
    ui_set(ui,"public-key",public_key);ui_set(ui,"identity-fingerprint",fingerprint);ui_set(ui,"enrollment-error",error);
    gtk_widget_set_visible(g_hash_table_lookup(ui->fields,"enrollment-error"),error[0]!=0);
    gtk_widget_set_sensitive(g_hash_table_lookup(ui->fields,"copy-key"),public_key[0]!=0);
    gtk_widget_set_sensitive(g_hash_table_lookup(ui->fields,"copy-identity"),fingerprint[0]!=0);
}
void ui_enrollment_reset(UI *ui) {
    ui->resetting_enrollment=TRUE;
    gtk_expander_set_expanded(GTK_EXPANDER(g_hash_table_lookup(ui->fields,"enrollment")),FALSE);
    ui->resetting_enrollment=FALSE;
    ui_enrollment(ui,"","","");
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
    gtk_window_set_default_icon_name("io.github.keyparcel.keyparcel");
    GtkCssProvider *style = gtk_css_provider_new();
    gtk_css_provider_load_from_data(style,
        ".keyparcel .brand { font-size: 1.5em; font-weight: 700; }"
        ".keyparcel .page-title { font-size: 1.7em; font-weight: 700; margin-bottom: 2px; }"
        ".keyparcel .page { padding: 24px; }"
        ".keyparcel .header { padding: 20px 24px 16px; }"
        ".keyparcel progressbar.transfer-progress trough { min-width: 72px; }"
        ".keyparcel .navigation { padding: 0 24px 16px; border-bottom: 1px solid alpha(@theme_fg_color, 0.12); }"
        ".keyparcel .navigation button { border-radius: 18px; padding: 7px 18px; }"
        ".keyparcel .navigation button:checked:not(:disabled), .keyparcel button.suggested-action:not(:disabled) { background-image: none; background-color: #103F4D; color: #F7F5F0; border-color: alpha(@theme_fg_color, 0.35); box-shadow: none; }"
        ".keyparcel .navigation button:checked:not(:disabled) { border-color: #DDA348; font-weight: 600; }"
        ".keyparcel button.suggested-action:hover:not(:disabled) { background-color: shade(#103F4D, 1.12); }"
        ".keyparcel button.suggested-action:active:not(:disabled) { background-color: shade(#103F4D, 0.9); }"
        ".keyparcel button:focus-visible { outline-color: #A5782A; outline-style: solid; outline-width: 2px; outline-offset: 2px; }"
        ".keyparcel .card { background: @theme_base_color; border: 1px solid alpha(@theme_fg_color, 0.14); border-radius: 12px; padding: 16px; }"
        ".keyparcel .message-editor { border: 1px solid alpha(@theme_fg_color, 0.22); border-radius: 8px; }"
        ".keyparcel .key-material { font-family: monospace; font-size: 0.9em; }"
        ".keyparcel .confirmation { padding: 10px 16px; border-radius: 8px; }"
        ".keyparcel .connection-status { font-size: 1.2em; }"
        ".keyparcel .build-version { font-size: 0.85em; }"
        ".keyparcel .connection-status.success { color: #287d38; }"
        ".keyparcel .connection-status.warning { color: #947000; }"
        ".keyparcel .connection-status.error { color: #c01c28; }"
        ".keyparcel button { padding: 7px 14px; border-radius: 10px; }"
        ".keyparcel .settings-info > button { padding: 2px; min-width: 24px; min-height: 24px; border-color: transparent; background: transparent; box-shadow: none; }"
        ".keyparcel .settings-info > button:hover { background: alpha(@theme_fg_color, 0.08); }", -1);
    gtk_style_context_add_provider_for_display(gdk_display_get_default(), GTK_STYLE_PROVIDER(style), GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);
    g_object_unref(style);
    UI *ui = g_new0(UI, 1);
    ui->refs = 1; ui->handle = handle;
    ui->fields = g_hash_table_new_full(g_str_hash, g_str_equal, g_free, NULL);
    ui->inbox_rows = g_hash_table_new_full(g_str_hash,g_str_equal,g_free,NULL);
    ui->device_rows = g_hash_table_new_full(g_str_hash,g_str_equal,g_free,NULL);
    ui->app = gtk_application_new("io.github.keyparcel.keyparcel", G_APPLICATION_NON_UNIQUE);
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
    gtk_widget_set_hexpand(brand, FALSE); gtk_box_append(GTK_BOX(brand_row), brand);
    GtkWidget *sync = label("○"); remember(ui, "sync", sync);
    gtk_label_set_selectable(GTK_LABEL(sync), FALSE);
    gtk_widget_add_css_class(sync, "dim-label"); gtk_widget_add_css_class(sync, "connection-status");
    gtk_label_set_width_chars(GTK_LABEL(sync),2); gtk_widget_set_focusable(sync,TRUE);
    accessible(sync, "Connection status"); gtk_box_append(GTK_BOX(brand_row), sync);
    // Measure the active controls while idle so showing Cancel cannot grow the header.
    ui->progress = gtk_stack_new();
    gtk_stack_set_hhomogeneous(GTK_STACK(ui->progress), FALSE);
    GtkWidget *work = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 6);
    ui->spinner = gtk_spinner_new(); ui->status = label("");
    gtk_label_set_wrap(GTK_LABEL(ui->status),FALSE);
    ui->transfer_bar = gtk_progress_bar_new();
    accessible(ui->transfer_bar,"Transfer progress");
    gtk_widget_add_css_class(ui->transfer_bar,"transfer-progress");
    gtk_widget_set_valign(ui->transfer_bar,GTK_ALIGN_CENTER);
    gtk_widget_set_size_request(ui->transfer_bar,72,-1);
    gtk_widget_set_visible(ui->transfer_bar,FALSE);
    accessible(ui->status,"Operation progress");
    ui->cancel = button(ui, "Cancel operation", "cancel", "");
    gtk_widget_add_css_class(ui->cancel, "flat");
    gtk_box_append(GTK_BOX(work), ui->transfer_bar); gtk_box_append(GTK_BOX(work), ui->spinner); gtk_box_append(GTK_BOX(work), ui->status);
    gtk_box_append(GTK_BOX(work), ui->cancel);
    gtk_stack_add_named(GTK_STACK(ui->progress), gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 0), "idle");
    gtk_stack_add_named(GTK_STACK(ui->progress), work, "active");
    gtk_box_append(GTK_BOX(brand_row),ui->progress);
    gtk_box_append(GTK_BOX(header), brand_row);
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
    GtkWidget *items = gtk_box_new(GTK_ORIENTATION_VERTICAL, 14); remember(ui, "items", items);
    gtk_box_append(GTK_BOX(inbox), items);

    GtkWidget *settings = page(ui, "settings", "Settings — shared with the CLI");
    info_label(ui,settings,"SSH target","Enter user@host or an SSH alias. The list contains keyparcel configurations previously saved through the CLI or this GTK app.","SSH target information");
    GtkWidget *hosts = gtk_combo_box_text_new_with_entry(); remember(ui, "hosts", hosts);
    remember(ui, "target", gtk_combo_box_get_child(GTK_COMBO_BOX(hosts)));
    accessible(gtk_combo_box_get_child(GTK_COMBO_BOX(hosts)), "SSH target"); gtk_box_append(GTK_BOX(settings), hosts);
    g_signal_connect(hosts,"changed",G_CALLBACK(host_selected),ui);
    g_signal_connect(hosts,"notify::popup-shown",G_CALLBACK(host_popup_changed),ui);
    info_label(ui,settings,"Identity file","Choose an unencrypted Ed25519 or RSA private key (RSA: at least 2048 bits), not the .pub file. Passphrase-protected keys aren’t supported yet.","Identity file information");
    GtkWidget *identity_row=gtk_box_new(GTK_ORIENTATION_HORIZONTAL,8), *identity=gtk_entry_new();
    remember(ui,"identity",identity);accessible(identity,"Identity file");
    gtk_entry_set_placeholder_text(GTK_ENTRY(identity),"~/.ssh/id_ed25519");gtk_widget_set_hexpand(identity,TRUE);
    g_signal_connect(identity,"changed",G_CALLBACK(identity_changed),ui);
    gtk_box_append(GTK_BOX(identity_row),identity);
    gtk_box_append(GTK_BOX(identity_row),button(ui,"Browse…","choose-identity",""));gtk_box_append(GTK_BOX(settings),identity_row);
    gtk_box_append(GTK_BOX(settings),button(ui,"Save & connect","save-connect",""));
    GtkWidget *enrollment=gtk_expander_new("Show enrollment details"), *enrollment_box=gtk_box_new(GTK_ORIENTATION_VERTICAL,8);
    remember(ui,"enrollment",enrollment);accessible(enrollment,"Show enrollment details");
    GtkWidget *pub=label(""), *fp=label(""), *enrollment_error=label("");
    remember(ui,"public-key",pub);remember(ui,"identity-fingerprint",fp);remember(ui,"enrollment-error",enrollment_error);
    gtk_widget_add_css_class(pub,"key-material");gtk_widget_add_css_class(fp,"key-material");gtk_widget_add_css_class(enrollment_error,"error");
    gtk_box_append(GTK_BOX(enrollment_box),enrollment_error);gtk_box_append(GTK_BOX(enrollment_box),pub);gtk_box_append(GTK_BOX(enrollment_box),fp);
    gtk_box_append(GTK_BOX(enrollment_box),label("Enroll this public key in the server's authorized_keys. Server approval is stored by OpenSSH."));
    GtkWidget *copy_actions=gtk_box_new(GTK_ORIENTATION_HORIZONTAL,8);
    GtkWidget *copy=button(ui,"Copy enrollment key","copy-key","");remember(ui,"copy-key",copy);gtk_box_append(GTK_BOX(copy_actions),copy);
    copy=button(ui,"Copy identity fingerprint","copy-identity","");remember(ui,"copy-identity",copy);gtk_box_append(GTK_BOX(copy_actions),copy);
    gtk_box_append(GTK_BOX(enrollment_box),copy_actions);gtk_expander_set_child(GTK_EXPANDER(enrollment),enrollment_box);gtk_box_append(GTK_BOX(settings),enrollment);
    g_signal_connect(enrollment,"notify::expanded",G_CALLBACK(enrollment_changed),ui);
    ui_enrollment_reset(ui);
    GtkWidget *advanced = gtk_expander_new("Advanced"); remember(ui, "advanced", advanced);
    GtkWidget *advanced_box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 8);
    info_label(ui,advanced_box,"Remote access method","SSH commands uses standard Unix commands on the server for faster operations with fewer network round trips. SFTP only uses the SFTP protocol and is more broadly supported. Automatic prefers SSH commands and falls back to SFTP when unsupported.","Remote access method information");
    GtkWidget *access_method=gtk_combo_box_text_new();
    gtk_combo_box_text_append(GTK_COMBO_BOX_TEXT(access_method),"auto","Automatic");
    gtk_combo_box_text_append(GTK_COMBO_BOX_TEXT(access_method),"ssh","SSH commands");
    gtk_combo_box_text_append(GTK_COMBO_BOX_TEXT(access_method),"sftp","SFTP only");
    gtk_combo_box_set_active_id(GTK_COMBO_BOX(access_method),"auto");
    accessible(access_method,"Remote access method");remember(ui,"access-method",access_method);gtk_box_append(GTK_BOX(advanced_box),access_method);
    GtkWidget *method_hint=label("Save & connect to apply the remote access method");
    remember(ui,"access-method-hint",method_hint);gtk_widget_set_visible(method_hint,FALSE);gtk_box_append(GTK_BOX(advanced_box),method_hint);
    g_signal_connect(access_method,"changed",G_CALLBACK(access_method_changed),ui);
    field(ui, advanced_box, "root", "Remote storage");
    field(ui, advanced_box, "discovery", "Discovery file");
    GtkWidget *ssh_config = label(""); remember(ui, "ssh-config", ssh_config);
    gtk_widget_add_css_class(ssh_config,"dim-label");gtk_widget_add_css_class(ssh_config,"caption");
    gtk_widget_set_visible(ssh_config,FALSE);gtk_box_append(GTK_BOX(advanced_box),ssh_config);
    gtk_expander_set_child(GTK_EXPANDER(advanced), advanced_box); gtk_box_append(GTK_BOX(settings), advanced);
    GtkWidget *version = label(""); remember(ui,"build-version",version);
    gtk_label_set_selectable(GTK_LABEL(version),FALSE);
    gtk_widget_add_css_class(version,"dim-label");gtk_widget_add_css_class(version,"build-version");
    gtk_widget_set_vexpand(version,TRUE);gtk_widget_set_valign(version,GTK_ALIGN_END);
    gtk_widget_set_margin_top(version,16);gtk_box_append(GTK_BOX(settings),version);

    GtkWidget *devices = page(ui, "devices", "Devices and approvals");
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
    GtkWidget *editor=gtk_overlay_new();
    gtk_overlay_set_child(GTK_OVERLAY(editor),text_view(ui,"draft",TRUE));
    GtkWidget *paste=gtk_button_new_from_icon_name("edit-paste-symbolic");remember(ui,"paste-text",paste);
    accessible(paste,"Paste from clipboard");gtk_widget_set_tooltip_text(paste,"Paste from clipboard");
    gtk_widget_add_css_class(paste,"flat");gtk_widget_set_focus_on_click(paste,FALSE);
    gtk_widget_set_halign(paste,GTK_ALIGN_END);gtk_widget_set_valign(paste,GTK_ALIGN_START);
    gtk_widget_set_margin_top(paste,4);gtk_widget_set_margin_end(paste,4);
    gtk_text_view_set_right_margin(GTK_TEXT_VIEW(g_hash_table_lookup(ui->fields,"draft")),52);
    g_signal_connect(paste,"clicked",G_CALLBACK(paste_clicked),ui);gtk_overlay_add_overlay(GTK_OVERLAY(editor),paste);
    gtk_box_append(GTK_BOX(send),editor);
    g_signal_connect(gtk_text_view_get_buffer(GTK_TEXT_VIEW(g_hash_table_lookup(ui->fields,"draft"))),
        "mark-set",G_CALLBACK(draft_cursor_changed),ui);
    GtkWidget *send_text = button(ui, "Send text", "send-text", ""); remember(ui, "send-text", send_text);
    gtk_widget_set_sensitive(send_text, FALSE); gtk_box_append(GTK_BOX(send), send_text);
    GtkWidget *selected = label("No file selected"); remember(ui, "selected-file", selected); gtk_box_append(GTK_BOX(send), selected);
    GtkWidget *file_actions = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    GtkWidget *choose = button(ui, "Choose file", "choose-file", ""); remember(ui,"choose-file",choose); gtk_box_append(GTK_BOX(file_actions),choose);
    GtkWidget *send_file = button(ui, "Send file", "send-file", ""); remember(ui, "send-file", send_file);
    gtk_widget_set_sensitive(send_file, FALSE); gtk_box_append(GTK_BOX(file_actions), send_file);
    g_signal_connect(gtk_text_view_get_buffer(GTK_TEXT_VIEW(g_hash_table_lookup(ui->fields, "draft"))),
        "changed", G_CALLBACK(draft_changed), ui);
    gtk_box_append(GTK_BOX(send), file_actions);
    gtk_box_append(GTK_BOX(send), button(ui, "Discard draft", "discard-draft", ""));

    GtkWidget *view = page(ui, "text", "Parcel");
    GtkWidget *retained = label("The remote copy remains until you choose Remove & close.");
    gtk_widget_add_css_class(retained,"dim-label"); gtk_box_append(GTK_BOX(view),retained);
    GtkWidget *preview_header = gtk_box_new(GTK_ORIENTATION_HORIZONTAL,0);
    GtkWidget *copy_message=icon_button(ui,"edit-copy-symbolic","Copy complete text","copy-message");
    remember(ui,"copy-message",copy_message);
    gtk_widget_set_hexpand(copy_message,TRUE);gtk_widget_set_halign(copy_message,GTK_ALIGN_END);
    gtk_box_append(GTK_BOX(preview_header),copy_message);gtk_box_append(GTK_BOX(view),preview_header);
    gtk_box_append(GTK_BOX(view), text_view(ui, "received", FALSE));
    g_signal_connect(g_hash_table_lookup(ui->fields, "received"), "copy-clipboard", G_CALLBACK(selected_copy), ui);
    GtkWidget *notice = label(""); remember(ui, "view-notice", notice); gtk_box_append(GTK_BOX(view), notice);
    gtk_box_append(GTK_BOX(view),button(ui,"Save file","save-view",""));
    gtk_box_append(GTK_BOX(view), button(ui, "Remove & close", "remove", ""));
    GtkWidget *viewer_frame = g_object_get_data(G_OBJECT(view),"frame");
    GtkWidget *viewer_footer = gtk_box_new(GTK_ORIENTATION_HORIZONTAL,0);
    GtkWidget *inbox_back = button(ui,"← Inbox","result-back","");
    accessible(inbox_back,"Back to inbox");gtk_widget_add_css_class(inbox_back,"flat");
    gtk_box_append(GTK_BOX(viewer_footer),inbox_back);gtk_box_append(GTK_BOX(viewer_frame),viewer_footer);
    GtkWidget *saved = page(ui, "saved", "File saved — remote copy retained");
    GtkWidget *dest = label(""); remember(ui, "saved-path", dest); gtk_box_append(GTK_BOX(saved), dest);
    gtk_box_append(GTK_BOX(saved), button(ui, "Remove & close", "remove", ""));
    GtkWidget *saved_back=button(ui,"Back to Inbox","result-back","");remember(ui,"saved-back",saved_back);gtk_box_append(GTK_BOX(saved),saved_back);

    GtkWidget *overlay=gtk_overlay_new();gtk_overlay_set_child(GTK_OVERLAY(overlay),outer);
    ui->confirmation=g_object_new(GTK_TYPE_BOX,"accessible-role",GTK_ACCESSIBLE_ROLE_STATUS,"orientation",GTK_ORIENTATION_HORIZONTAL,NULL);
    GtkWidget *confirmation_text=label("");
    gtk_label_set_selectable(GTK_LABEL(confirmation_text),FALSE);
    gtk_widget_set_can_target(confirmation_text,FALSE);gtk_widget_set_can_target(ui->confirmation,FALSE);
    g_object_set_data(G_OBJECT(ui->confirmation),"message",confirmation_text);
    gtk_box_append(GTK_BOX(ui->confirmation),confirmation_text);
    gtk_widget_add_css_class(ui->confirmation,"osd");gtk_widget_add_css_class(ui->confirmation,"confirmation");
    gtk_widget_set_margin_bottom(ui->confirmation,20);
    gtk_widget_set_halign(ui->confirmation,GTK_ALIGN_CENTER);gtk_widget_set_valign(ui->confirmation,GTK_ALIGN_END);
    gtk_widget_set_visible(ui->confirmation,FALSE);
    gtk_overlay_add_overlay(GTK_OVERLAY(overlay),ui->confirmation);
    gtk_window_set_child(ui->window, overlay);
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
    const char *old=gtk_stack_get_visible_child_name(ui->pages);
    if (g_strcmp0(old,name)!=0) { ui->paste_generation++;ui_message(ui,""); }
    gtk_stack_set_visible_child_name(ui->pages, name);
    const char *section = (strcmp(name, "text") == 0 || strcmp(name, "saved") == 0) ? "inbox" : name;
    char *nav = g_strdup_printf("nav-%s", section);
    gtk_toggle_button_set_active(g_hash_table_lookup(ui->fields, nav), TRUE); g_free(nav);
    gtk_widget_set_visible(g_hash_table_lookup(ui->fields,"back"),strcmp(name,"text")!=0);
    gtk_widget_set_sensitive(g_hash_table_lookup(ui->fields, "back"), strcmp(name, "settings") == 0 || strcmp(name, "text") == 0 || strcmp(name, "saved") == 0);
}
void ui_message(UI *ui, const char *text) {
    char *key=g_strdup_printf("error-%s",gtk_stack_get_visible_child_name(ui->pages));
    GtkWidget *box=g_hash_table_lookup(ui->fields,key);g_free(key);
    if (!box) return;
    gtk_label_set_text(GTK_LABEL(g_object_get_data(G_OBJECT(box),"message")),text);
    gtk_widget_set_visible(box,text[0]!=0);
}
void ui_confirmation(UI *ui,const char *text) {
    gtk_label_set_text(GTK_LABEL(g_object_get_data(G_OBJECT(ui->confirmation),"message")),text);
    accessible(ui->confirmation,text);
    gtk_widget_set_visible(ui->confirmation,text[0]!=0);
}
void ui_transfer_progress(UI *ui,const char *text,double fraction) {
    gtk_label_set_text(GTK_LABEL(ui->status),text);
    accessible(ui->status,"Operation progress");
    gtk_stack_set_visible_child_name(GTK_STACK(ui->progress),text[0] ? "active" : "idle");
    gboolean determinate = text[0] && fraction >= 0;
    gtk_widget_set_visible(ui->transfer_bar,determinate);
    gtk_widget_set_visible(ui->spinner,!determinate);
    if (determinate) gtk_progress_bar_set_fraction(GTK_PROGRESS_BAR(ui->transfer_bar),fraction);
    if (text[0] && !determinate) gtk_spinner_start(GTK_SPINNER(ui->spinner)); else gtk_spinner_stop(GTK_SPINNER(ui->spinner));
}
void ui_progress(UI *ui,const char *text) { ui_transfer_progress(ui,text,-1); }

double ui_transfer_fraction(UI *ui) {
    return gtk_widget_get_visible(ui->transfer_bar) ? gtk_progress_bar_get_fraction(GTK_PROGRESS_BAR(ui->transfer_bar)) : -1;
}
void ui_busy(UI *ui, int busy) {
    if (busy) ui->paste_generation++;
    gtk_widget_set_sensitive(ui->body, !busy); gtk_widget_set_sensitive(ui->cancel, busy);
    if (!busy) ui_progress(ui,"");
}
const char *ui_operation_progress(UI *ui) {
    if ((!gtk_spinner_get_spinning(GTK_SPINNER(ui->spinner)) && !gtk_widget_get_visible(ui->transfer_bar)) ||
        !gtk_widget_is_sensitive(ui->cancel) ||
        strcmp(gtk_stack_get_visible_child_name(GTK_STACK(ui->progress)), "active") != 0) return "";
    return gtk_label_get_text(GTK_LABEL(ui->status));
}
int ui_activate_cancel(UI *ui) {
    if (!gtk_widget_is_sensitive(ui->cancel)) return 0;
    g_signal_emit_by_name(ui->cancel, "clicked");
    return 1;
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
    if (GTK_IS_COMBO_BOX(w)) gtk_combo_box_set_active_id(GTK_COMBO_BOX(w),text);
    else if (GTK_IS_EDITABLE(w)) gtk_editable_set_text(GTK_EDITABLE(w), text);
    else if (GTK_IS_LABEL(w)) gtk_label_set_text(GTK_LABEL(w), text);
    if (strcmp(name,"ssh-config") == 0) gtk_widget_set_visible(w,text[0]!=0);
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
void ui_host(UI *ui, const char *name) { gtk_combo_box_text_append(GTK_COMBO_BOX_TEXT(g_hash_table_lookup(ui->fields, "hosts")), name, name); }
void ui_send_ready(UI *ui, int has_file) {
    ui->has_file = has_file;
    gboolean selected = ui->send_count > 0;
    GtkTextBuffer *draft = gtk_text_view_get_buffer(GTK_TEXT_VIEW(g_hash_table_lookup(ui->fields, "draft")));
    gtk_widget_set_sensitive(g_hash_table_lookup(ui->fields, "send-text"), selected && !ui->send_locked && gtk_text_buffer_get_char_count(draft) > 0);
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
    ui->paste_generation++;
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

void ui_connection(UI *ui, const char *state) {
    GtkWidget *w = g_hash_table_lookup(ui->fields,"sync");
    const char *symbol="○", *color="dim-label";
    if (strcmp(state,"Connected")==0) { symbol="●";color="success"; }
    else if (strcmp(state,"Connecting")==0) { symbol="↻";color="warning"; }
    else if (strcmp(state,"Disconnected")==0) { symbol="⊗";color="error"; }
    const char *classes[]={"dim-label","success","warning","error"};
    for (int i=0;i<4;i++) gtk_widget_remove_css_class(w,classes[i]);
    gtk_widget_add_css_class(w,color);gtk_label_set_text(GTK_LABEL(w),symbol);
    accessible(w,"Connection status");
    gtk_widget_set_tooltip_text(w,state);
    gtk_accessible_update_property(GTK_ACCESSIBLE(w),GTK_ACCESSIBLE_PROPERTY_DESCRIPTION,state,-1);
}
void ui_refreshing(UI *ui, int refreshing) {
    gtk_widget_set_sensitive(ui->cancel,refreshing);
    ui_progress(ui,refreshing ? "Refreshing…" : "");
}

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
    if (locked) ui->paste_generation++;
    ui->send_locked=locked;ui->send_count=count;
    gtk_widget_set_sensitive(g_hash_table_lookup(ui->fields,"paste-text"),!locked);
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

void ui_present(UI *ui) { g_application_activate(G_APPLICATION(ui->app)); }
void ui_flush(void) { while (g_main_context_pending(NULL)) g_main_context_iteration(NULL,FALSE); }
InfoState ui_info_state(UI *ui,const char *name) {
    InfoState result={0};
    GtkWidget *popover=g_hash_table_lookup(ui->fields,name), *button=gtk_widget_get_parent(popover);
    graphene_rect_t bounds;
    if (gtk_widget_compute_bounds(button,GTK_WIDGET(ui->window),&bounds)) {
        result.x=bounds.origin.x;result.y=bounds.origin.y;
        result.width=bounds.size.width;result.height=bounds.size.height;
    }
    result.mapped=gtk_widget_get_mapped(button);
    result.focused=(gtk_widget_get_state_flags(button)&GTK_STATE_FLAG_FOCUS_WITHIN)!=0;
    result.open=gtk_widget_get_visible(popover);
    result.hovered=(gtk_widget_get_state_flags(gtk_widget_get_first_child(button))&GTK_STATE_FLAG_PRELIGHT)!=0;
    result.pressed=(gtk_widget_get_state_flags(gtk_widget_get_first_child(button))&GTK_STATE_FLAG_ACTIVE)!=0;
    return result;
}

void ui_access_hint(UI *ui,int show) { gtk_widget_set_visible(g_hash_table_lookup(ui->fields,"access-method-hint"),show); }

int ui_access_pending(UI *ui) { return gtk_widget_get_visible(g_hash_table_lookup(ui->fields,"access-method-hint")); }

void ui_draft_select(UI *ui,const char *text,int start,int end) {
    GtkTextBuffer *buffer=gtk_text_view_get_buffer(GTK_TEXT_VIEW(g_hash_table_lookup(ui->fields,"draft")));
    gtk_text_buffer_set_text(buffer,text,-1);
    GtkTextIter a,b;gtk_text_buffer_get_iter_at_offset(buffer,&a,start);gtk_text_buffer_get_iter_at_offset(buffer,&b,end);
    gtk_text_buffer_select_range(buffer,&a,&b);
}
int ui_draft_cursor(UI *ui) {
    GtkTextBuffer *buffer=gtk_text_view_get_buffer(GTK_TEXT_VIEW(g_hash_table_lookup(ui->fields,"draft")));
    GtkTextIter iter;gtk_text_buffer_get_iter_at_mark(buffer,&iter,gtk_text_buffer_get_insert(buffer));
    return gtk_text_iter_get_offset(&iter);
}
void ui_activate_paste(UI *ui) { g_signal_emit_by_name(g_hash_table_lookup(ui->fields,"paste-text"),"clicked"); }
InfoState ui_paste_state(UI *ui) {
    InfoState result={0};graphene_rect_t bounds;
    GtkWidget *button=g_hash_table_lookup(ui->fields,"paste-text");
    if (gtk_widget_compute_bounds(button,GTK_WIDGET(ui->window),&bounds)) {
        result.x=bounds.origin.x;result.y=bounds.origin.y;result.width=bounds.size.width;result.height=bounds.size.height;
    }
    result.mapped=gtk_widget_get_mapped(button);
    result.focused=gtk_widget_has_focus(g_hash_table_lookup(ui->fields,"draft"));
    return result;
}
