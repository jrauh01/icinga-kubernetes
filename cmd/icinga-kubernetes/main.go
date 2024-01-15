package main

import (
	"context"
	"flag"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/icinga/icinga-kubernetes/pkg/com"
	"github.com/icinga/icinga-kubernetes/pkg/database"
	"github.com/icinga/icinga-kubernetes/pkg/periodic"
	schemav1 "github.com/icinga/icinga-kubernetes/pkg/schema/v1"
	"github.com/icinga/icinga-kubernetes/pkg/sync"
	syncv1 "github.com/icinga/icinga-kubernetes/pkg/sync/v1"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"os"
	"path/filepath"
	"time"
)

func main() {
	runtime.ReallyCrash = true

	var config string
	var kubeconfig string
	var master string

	userHomeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("error getting user home dir: %v\n", err)
		os.Exit(1)
	}
	kubeConfigPath := filepath.Join(userHomeDir, ".kube", "config")
	klog.InitFlags(nil)
	flag.StringVar(&kubeconfig, "kubeconfig", kubeConfigPath, "absolute path to the kubeconfig file")
	flag.StringVar(&master, "master", "", "master url")
	flag.StringVar(&config, "config", "./config.yml", "path to the config file")
	flag.Parse()

	clientconfig, err := clientcmd.BuildConfigFromFlags(master, kubeconfig)
	if err != nil {
		klog.Fatal(err)
	}

	clientset, err := kubernetes.NewForConfig(clientconfig)
	if err != nil {
		klog.Fatal(err)
	}

	factory := informers.NewSharedInformerFactory(clientset, 0)
	log := klog.NewKlogr()

	d, err := database.FromYAMLFile(config)
	if err != nil {
		klog.Fatal(err)
	}
	db, err := database.NewFromConfig(d, log.WithName("database"))
	if err != nil {
		klog.Fatal(err)
	}
	if !db.Connect() {
		return
	}

	g, ctx := errgroup.WithContext(context.Background())
	g.Go(func() error {
		s := syncv1.NewSync(db, factory.Core().V1().Namespaces().Informer(), log.WithName("namespaces"), schemav1.NewNamespace)

		return s.Run(ctx)
	})
	g.Go(func() error {
		s := syncv1.NewSync(db, factory.Core().V1().Nodes().Informer(), log.WithName("nodes"), schemav1.NewNode)

		return s.Run(ctx)
	})
	g.Go(func() error {
		pods := make(chan any)
		deletePodIds := make(chan interface{})
		defer close(pods)
		defer close(deletePodIds)

		schemav1.SyncContainers(ctx, db, g, pods, deletePodIds)

		f := schemav1.NewPodFactory(clientset)
		s := syncv1.NewSync(db, factory.Core().V1().Pods().Informer(), log.WithName("pods"), f.New)

		return s.Run(ctx, sync.WithOnUpsert(com.ForwardBulk(pods)), sync.WithOnDelete(com.ForwardBulk(deletePodIds)))
	})
	g.Go(func() error {
		s := syncv1.NewSync(db, factory.Apps().V1().Deployments().Informer(), log.WithName("deployments"), schemav1.NewDeployment)

		return s.Run(ctx)
	})
	g.Go(func() error {
		s := syncv1.NewSync(db, factory.Apps().V1().DaemonSets().Informer(), log.WithName("daemon-sets"), schemav1.NewDaemonSet)

		return s.Run(ctx)
	})
	g.Go(func() error {
		s := syncv1.NewSync(db, factory.Apps().V1().ReplicaSets().Informer(), log.WithName("replica-sets"), schemav1.NewReplicaSet)

		return s.Run(ctx)
	})
	g.Go(func() error {
		s := syncv1.NewSync(db, factory.Apps().V1().StatefulSets().Informer(), log.WithName("stateful-sets"), schemav1.NewStatefulSet)

		return s.Run(ctx)
	})
	g.Go(func() error {
		s := syncv1.NewSync(db, factory.Core().V1().Services().Informer(), log.WithName("services"), schemav1.NewService)

		return s.Run(ctx)
	})
	g.Go(func() error {
		s := syncv1.NewSync(db, factory.Discovery().V1().EndpointSlices().Informer(), log.WithName("endpoints"), schemav1.NewEndpointSlice)

		return s.Run(ctx)
	})
	g.Go(func() error {
		s := syncv1.NewSync(db, factory.Core().V1().Secrets().Informer(), log.WithName("secrets"), schemav1.NewSecret)
		return s.Run(ctx)
	})
	g.Go(func() error {
		s := syncv1.NewSync(db, factory.Core().V1().ConfigMaps().Informer(), log.WithName("config-maps"), schemav1.NewConfigMap)

		return s.Run(ctx)
	})
	g.Go(func() error {
		s := syncv1.NewSync(db, factory.Events().V1().Events().Informer(), log.WithName("events"), schemav1.NewEvent)

		return s.Run(ctx, sync.WithNoDelete(), sync.WithNoWarumup())
	})
	g.Go(func() error {
		s := syncv1.NewSync(db, factory.Core().V1().PersistentVolumeClaims().Informer(), log.WithName("pvcs"), schemav1.NewPvc)

		return s.Run(ctx)
	})
	g.Go(func() error {
		s := syncv1.NewSync(db, factory.Core().V1().PersistentVolumes().Informer(), log.WithName("persistent-volumes"), schemav1.NewPersistentVolume)

		return s.Run(ctx)
	})
	g.Go(func() error {
		s := syncv1.NewSync(db, factory.Batch().V1().Jobs().Informer(), log.WithName("jobs"), schemav1.NewJob)

		return s.Run(ctx)
	})
	g.Go(func() error {
		s := syncv1.NewSync(db, factory.Batch().V1().CronJobs().Informer(), log.WithName("cron-jobs"), schemav1.NewCronJob)

		return s.Run(ctx)
	})
	g.Go(func() error {
		s := syncv1.NewSync(db, factory.Networking().V1().Ingresses().Informer(), log.WithName("ingresses"), schemav1.NewIngress)

		return s.Run(ctx)
	})

	errs := make(chan error, 1)
	defer periodic.Start(ctx, time.Hour, func(tick periodic.Tick) {
		olderThan := tick.Time.AddDate(0, 0, -1)

		_, err := db.CleanupOlderThan(
			ctx, database.CleanupStmt{
				Table:  "event",
				PK:     "id",
				Column: "created",
			}, 5000, olderThan,
		)
		if err != nil {
			select {
			case errs <- err:
			case <-ctx.Done():
			}

			return
		}
	}, periodic.Immediate()).Stop()
	com.ErrgroupReceive(g, errs)

	if err := g.Wait(); err != nil {
		klog.Fatal(err)
	}
}
