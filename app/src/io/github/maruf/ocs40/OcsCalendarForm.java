package io.github.maruf.ocs40;

import java.util.Calendar;
import java.util.Date;

import javax.microedition.lcdui.ChoiceGroup;
import javax.microedition.lcdui.Command;
import javax.microedition.lcdui.CommandListener;
import javax.microedition.lcdui.DateField;
import javax.microedition.lcdui.Displayable;
import javax.microedition.lcdui.Form;
import javax.microedition.lcdui.TextField;

/**
 * "Takvime ekle" / "Add to calendar": a form prefilled from a message (from
 * OpenCode's entry line if there is one, else the start of the message and the
 * next full hour). The user checks it; only "Kaydet" writes it, on a worker
 * thread, to the phone's calendar or to-do list (OcsPim).
 */
final class OcsCalendarForm implements CommandListener, Runnable {

    private static final int[] ALARMS = { -1, 0, 15, 60, 24 * 60 };

    private final OcsS40MIDlet midlet;
    private final Displayable back;
    private final Form form;
    private final TextField title;
    private final ChoiceGroup kind;
    private final DateField when;
    private final ChoiceGroup alarm;
    private final Command saveCmd = new Command(OcsL.s("Kaydet", "Save"), Command.OK, 1);
    private final Command cancelCmd = new Command(OcsL.s("Vazgeç", "Cancel"), Command.BACK, 1);
    private boolean saving;

    // what run() writes
    private boolean todo;
    private String what;
    private long at;
    private int alarmMinutes;

    OcsCalendarForm(OcsS40MIDlet midlet, OcsChatSession.Entry e, Displayable back) {
        this.midlet = midlet;
        this.back = back;
        OcsCal c = OcsCal.parse(e.text);
        form = new Form(OcsL.s("Takvime ekle", "Add to calendar"));
        title = new TextField(OcsL.s("Başlık", "Title"), c != null ? c.title : OcsS40MIDlet.quote(e.text), 100,
                TextField.ANY);
        kind = new ChoiceGroup(OcsL.s("Nereye", "Where"), ChoiceGroup.EXCLUSIVE,
                new String[] { OcsL.s("Takvim", "Calendar"), OcsL.s("Yapılacaklar", "To-do list") }, null);
        kind.setSelectedIndex(c != null && c.todo ? 1 : 0, true);
        when = new DateField(OcsL.s("Tarih ve saat", "Date and time"), DateField.DATE_TIME);
        when.setDate(new Date(c != null ? c.when : nextHour()));
        alarm = new ChoiceGroup(OcsL.s("Hatırlatma (takvim)", "Alarm (calendar)"), ChoiceGroup.EXCLUSIVE,
                new String[] { OcsL.s("Yok", "None"), OcsL.s("Başlarken", "At the start"), OcsL.s("15 dakika önce", "15 minutes before"),
                    OcsL.s("1 saat önce", "1 hour before"), OcsL.s("1 gün önce", "1 day before") }, null);
        alarm.setSelectedIndex(2, true);
        form.append(title);
        form.append(kind);
        form.append(when);
        form.append(alarm);
        form.addCommand(saveCmd);
        form.addCommand(cancelCmd);
        form.setCommandListener(this);
    }

    void show() {
        midlet.display().setCurrent(form);
    }

    private static long nextHour() {
        Calendar c = Calendar.getInstance();
        c.setTime(new Date(System.currentTimeMillis() + 60L * 60 * 1000));
        c.set(Calendar.MINUTE, 0);
        c.set(Calendar.SECOND, 0);
        c.set(Calendar.MILLISECOND, 0);
        return c.getTime().getTime();
    }

    public void commandAction(Command cmd, Displayable d) {
        if (cmd == cancelCmd) {
            midlet.display().setCurrent(back);
            return;
        }
        if (cmd != saveCmd) {
            return;
        }
        String t = title.getString().trim();
        Date date = when.getDate();
        if (t.length() == 0 || date == null) {
            midlet.info(OcsL.s("Başlık ve tarih gerekli.", "A title and a date are needed."), form);
            return;
        }
        synchronized (this) {
            if (saving) {
                return;
            }
            saving = true;
            todo = kind.getSelectedIndex() == 1;
            what = t;
            at = date.getTime();
            int a = alarm.getSelectedIndex();
            alarmMinutes = a >= 0 && a < ALARMS.length ? ALARMS[a] : -1;
        }
        new Thread(this).start();
    }

    public void run() {
        boolean t;
        String w;
        long a;
        int m;
        synchronized (this) {
            t = todo;
            w = what;
            a = at;
            m = alarmMinutes;
        }
        String err = OcsPim.add(t, w, a, m);
        synchronized (this) {
            saving = false;
        }
        if (err != null) {
            midlet.info(err, form);
        } else {
            midlet.info((t ? OcsL.s("Yapılacaklara eklendi: ", "Added to the to-do list: ") + OcsText.local(a, false)
                    : OcsL.s("Takvime eklendi: ", "Added to the calendar: ") + OcsText.local(a, true)) + "\n" + w, back);
        }
    }
}
