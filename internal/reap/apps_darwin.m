//go:build darwin

#import <AppKit/AppKit.h>
#include <stdlib.h>

// Copy the GUI and accessory application PIDs in one snapshot. Filtering on
// activation policy includes menu-bar apps that the old launchedApplications
// API omits. The caller owns the returned malloc buffer; the autorelease pool
// bounds AppKit object lifetime without relying on a CLI application's run loop.
int hog_running_apps(int **pids, size_t *count) {
    *pids = NULL;
    *count = 0;
    @autoreleasepool {
        NSArray<NSRunningApplication *> *apps = [[NSWorkspace sharedWorkspace] runningApplications];
        if (apps == nil) {
            return 1;
        }
        if (apps.count == 0) {
            return 0;
        }
        int *buffer = calloc(apps.count, sizeof(int));
        if (buffer == NULL) {
            return 1;
        }
        for (NSRunningApplication *app in apps) {
            if (!app.terminated && app.processIdentifier > 0 &&
                (app.activationPolicy == NSApplicationActivationPolicyRegular ||
                 app.activationPolicy == NSApplicationActivationPolicyAccessory)) {
                buffer[(*count)++] = app.processIdentifier;
            }
        }
        *pids = buffer;
    }
    return 0;
}
